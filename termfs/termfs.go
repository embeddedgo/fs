// Copyright 2020 The Embedded Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package termfs provides a lightwait implementation of terminal device
// filesystem.
package termfs

import (
	"io"
	"io/fs"
	"sync"
	"syscall"
	"time"
)

// An FS provides a file system that represents a terminal device. As the
// embeded systems rarely require more than one terminal device (console) the
// FS is very simple and provides only one device file "." which can be opened,
// written and read concurenly by multiple goroutines.
type FS struct {
	r     io.Reader
	w     io.Writer
	name  string
	rmu   sync.Mutex
	wmu   sync.Mutex
	line  []byte
	rpos  int
	ansi  [7]byte
	flags CharMap
}

// New returns a new terminal file system named name. The r and w correspond
// to the terminal input and output device.
func New(name string, r io.Reader, w io.Writer) *FS {
	return &FS{r: r, w: w, name: name}
}

type CharMap uint8

const (
	InCRLF    CharMap = 1 << 0 // map input "\r" to "\n"
	OutLFCRLF CharMap = 1 << 3 // map output "\n" to "\r\n"

	mapFlags = (InCRLF | OutLFCRLF)
	eof      = 1 << 6
	echo     = 1 << 7
)

func (fsys *FS) CharMap() CharMap {
	fsys.rmu.Lock()
	fsys.wmu.Lock()
	cmap := fsys.flags & mapFlags
	fsys.wmu.Unlock()
	fsys.rmu.Unlock()
	return cmap
}

func (fsys *FS) SetCharMap(cmap CharMap) {
	fsys.rmu.Lock()
	fsys.wmu.Lock()
	fsys.flags = fsys.flags&^mapFlags | cmap&mapFlags
	fsys.wmu.Unlock()
	fsys.rmu.Unlock()
}

// Echo returns the echo configuration.
func (fsys *FS) Echo() bool {
	fsys.rmu.Lock()
	echo := fsys.flags&echo != 0
	fsys.rmu.Unlock()
	return echo
}

// SetEcho enables/disables echoing of input data. Data are echoed by fs.File
// Read method. The echo is a confirmation that the reading goroutine is ready
// to consume data.
func (fsys *FS) SetEcho(on bool) {
	fsys.rmu.Lock()
	if on {
		fsys.flags |= echo
	} else {
		fsys.flags &^= echo
	}
	fsys.flags |= echo
	fsys.rmu.Unlock()
}

// LineMode returns the configuration of line mode.
func (fsys *FS) LineMode() (enabled bool, maxLen int) {
	fsys.rmu.Lock()
	enabled = fsys.ansi[0] != 0
	maxLen = cap(fsys.line)
	fsys.rmu.Unlock()
	return
}

// SetLineMode allows to enable/disable the line mode and change the size of
// the internal line buffer. The default line buffer has zero size. Use
// maxLen > 0 to allocate a new one, maxLen == 0 to free it and maxLen < 0 to
// leave the line buffer unchanged.
//
// In the line mode the terminal input is buffered until new-line character
// received. Small subset of ANSI terminal codes is supported to enable editing
// the line before passing it to the reading goroutine. There is also simple one
// line history implemented (use up, down arrows).
func (fsys *FS) SetLineMode(enable bool, maxLen int) {
	fsys.rmu.Lock()
	if enable {
		fsys.ansi[0] = '\b' // useful to move cursor back in ANSI DCH sequence
		fsys.ansi[1] = esc  // ANSI escape character
		fsys.ansi[2] = '['  // ANSI Control Sequence Introducer
	} else {
		fsys.ansi[0] = 0
	}
	fsys.rpos = -1
	if maxLen >= 0 {
		if maxLen == 0 {
			fsys.line = nil
		} else {
			fsys.line = make([]byte, 0, maxLen)
		}
	}
	fsys.rmu.Unlock()
}

// OpenWithFinalizer implements the rtos.FS OpenWithFinalizer method. The name
// must be ".", the flag can be O_RDWR, O_RDONLY, O_WRONLY, the perm is ignored.
func (fsys *FS) OpenWithFinalizer(name string, flag int, perm fs.FileMode, closed func()) (fs.File, error) {
	if name != "." {
		return nil, &fs.PathError{Op: "open", Path: name, Err: syscall.ENOENT}
	}
	if flag&^(syscall.O_RDONLY|syscall.O_WRONLY|syscall.O_RDWR) != 0 {
		return nil, &fs.PathError{Op: "open", Path: name, Err: syscall.EINVAL}
	}
	return &file{fsys, flag, closed}, nil
}

func nop() {}

// Open implements the fs.FS Open method.
func (fsys *FS) Open(name string) (fs.File, error) {
	return fsys.OpenWithFinalizer(name, syscall.O_RDONLY, 0, nop)
}

// Type implements the rtos.FS Type method
func (fsys *FS) Type() string { return "term" }

// Name implements the rtos.FS Name method
func (fsys *FS) Name() string { return fsys.name }

// Usage implements the rtos.FS Usage method
func (fsys *FS) Usage() (int, int, int64, int64) { return -1, -1, -1, -1 }

type file struct {
	fs     *FS
	flag   int
	closed func()
}

func wrapErr(op string, err error) error {
	return &fs.PathError{Op: op, Path: ".", Err: err}
}

func (f *file) Read(p []byte) (n int, err error) {
	if f.flag == syscall.O_WRONLY {
		err = syscall.EBADF
		goto end
	}
	if len(p) == 0 {
		return 0, nil
	}
	{
		f.fs.rmu.Lock()
		lineMode := f.fs.ansi[0] != 0
		flags := f.fs.flags
		if f.closed == nil {
			err = syscall.EBADF
		} else if !lineMode {
			n, err = f.fs.r.Read(p)
		} else {
			n, err = readLine(f, p)
		}
		f.fs.rmu.Unlock()
		if !lineMode {
			if flags&InCRLF != 0 {
				for i := 0; i < n; i++ {
					if p[i] == '\r' {
						p[i] = '\n'
					}
				}
			}
			if flags&echo != 0 {
				_, err = write(f, p[:n])
			}
		}
	}
end:
	if err != nil && err != io.EOF {
		err = wrapErr("read", err)
	}
	return n, err
}

var crlf = [...]byte{'\r', '\n'}

func write(f *file, p []byte) (n int, err error) {
	if len(p) == 0 {
		return 0, nil
	}
	f.fs.wmu.Lock()
	if f.closed == nil {
		err = syscall.EBADF
		goto end
	}
	if f.fs.flags&OutLFCRLF == 0 {
		n, err = f.fs.w.Write(p)
		goto end
	}
	for {
		m := n
		for {
			if p[m] == '\n' {
				break
			}
			m++
			if m == len(p) {
				break
			}
		}
		if m != n {
			m, err = f.fs.w.Write(p[n:m])
			n += m
			if err != nil {
				break
			}
			if n == len(p) {
				break
			}
		}
		if _, err = f.fs.w.Write(crlf[:]); err != nil {
			break
		}
		n++
		if n == len(p) {
			break
		}
	}
end:
	f.fs.wmu.Unlock()
	if err != nil {
		err = wrapErr("write", err)
	}
	return n, err
}

func (f *file) Write(p []byte) (int, error) {
	if f.flag == syscall.O_RDONLY {
		return 0, wrapErr("write", syscall.EBADF)
	}
	return write(f, p)
}

func (f *file) Stat() (fs.FileInfo, error) {
	return &fileinfo{}, nil
}

func (f *file) Close() (err error) {
	// we assume that closing a terminal file is rare operation so we use the
	// following expensive locking sequence instead of an additional f.lock
	f.fs.rmu.Lock()
	f.fs.wmu.Lock()
	if f.closed == nil {
		err = wrapErr("close", syscall.EBADF)
	} else {
		f.closed()
		f.closed = nil
	}
	f.fs.wmu.Unlock()
	f.fs.rmu.Unlock()
	return err
}

type fileinfo struct{}

func (fi *fileinfo) Name() string       { return "." }
func (fi *fileinfo) Size() int64        { return 0 }
func (fi *fileinfo) Mode() fs.FileMode  { return fs.ModeDevice | 0222 }
func (fi *fileinfo) ModTime() time.Time { return time.Time{} }
func (fi *fileinfo) IsDir() bool        { return false }
func (fi *fileinfo) Sys() interface{}   { return nil }
