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
	rlock sync.Mutex
	wlock sync.Mutex
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
	fsys.rlock.Lock()
	fsys.wlock.Lock()
	cmap := fsys.flags & mapFlags
	fsys.wlock.Unlock()
	fsys.rlock.Unlock()
	return cmap
}

func (fsys *FS) SetCharMap(cmap CharMap) {
	fsys.rlock.Lock()
	fsys.wlock.Lock()
	fsys.flags = fsys.flags&^mapFlags | cmap&mapFlags
	fsys.wlock.Unlock()
	fsys.rlock.Unlock()
}

// Echo returns the echo configuration.
func (fsys *FS) Echo() bool {
	fsys.rlock.Lock()
	echo := fsys.flags&echo != 0
	fsys.rlock.Unlock()
	return echo
}

// SetEcho enables/disables echoing of input data. Data are echoed by fs.File
// Read method. The echo is a confirmation that the reading goroutine is ready
// to consume data.
func (fsys *FS) SetEcho(on bool) {
	fsys.rlock.Lock()
	if on {
		fsys.flags |= echo
	} else {
		fsys.flags &^= echo
	}
	fsys.flags |= echo
	fsys.rlock.Unlock()
}

// LineMode returns the configuration of line mode.
func (fsys *FS) LineMode() (enabled bool, maxLen int) {
	fsys.rlock.Lock()
	enabled = fsys.ansi[0] != 0
	maxLen = cap(fsys.line)
	fsys.rlock.Unlock()
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
	fsys.rlock.Lock()
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
	fsys.rlock.Unlock()
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
	if err != nil && err != io.EOF {
		return &fs.PathError{Op: op, Path: ".", Err: err}
	}
	return err
}

func (f *file) Read(p []byte) (n int, err error) {
	if f.flag == syscall.O_WRONLY {
		return 0, wrapErr("read", syscall.ENOTSUP)
	}
	if len(p) == 0 {
		return 0, nil
	}
	f.fs.rlock.Lock()
	lineMode := f.fs.ansi[0] != 0
	if f.closed == nil {
		err = wrapErr("read", syscall.EBADF)
	} else if !lineMode {
		n, err = f.fs.r.Read(p)
	} else {
		n, err = readLine(f, p)
	}
	f.fs.rlock.Unlock()
	if lineMode {
		return n, err
	}
	if f.fs.flags&InCRLF != 0 {
		for i := 0; i < n; i++ {
			if p[i] == '\r' {
				p[i] = '\n'
			}
		}
	}
	if f.fs.flags&echo == 0 || err != nil {
		return n, wrapErr("read", err)
	}
	return write(f, p[:n])
}

var crlf = [...]byte{'\r', '\n'}

func write(f *file, p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	f.fs.wlock.Lock()
	defer f.fs.wlock.Unlock()
	if f.closed == nil {
		return 0, wrapErr("write", syscall.EBADF)
	}
	if f.fs.flags&OutLFCRLF == 0 {
		n, err := f.fs.w.Write(p)
		err = wrapErr("write", err)
		return n, err
	}
	n := 0
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
			m, err := f.fs.w.Write(p[n:m])
			n += m
			if err != nil {
				return n, wrapErr("write", err)
			}
			if n == len(p) {
				return n, nil
			}
		}
		if _, err := f.fs.w.Write(crlf[:]); err != nil {
			return n, wrapErr("write", err)
		}
		n++
		if n == len(p) {
			return n, nil
		}
	}
}

func (f *file) Write(p []byte) (int, error) {
	if f.flag == syscall.O_RDONLY {
		return 0, wrapErr("write", syscall.ENOTSUP)
	}
	return write(f, p)
}

func (f *file) Stat() (fs.FileInfo, error) {
	return &fileinfo{}, nil
}

func (f *file) Close() error {
	// we assume that closing a terminal file is rare operation so we use the
	// following expensive locking sequence instead of an additional f.lock
	f.fs.rlock.Lock()
	f.fs.wlock.Lock()
	defer f.fs.wlock.Unlock()
	defer f.fs.rlock.Unlock()
	if f.closed == nil {
		return wrapErr("close", syscall.EBADF)
	}
	f.closed()
	f.closed = nil
	return nil
}

type fileinfo struct{}

func (fi *fileinfo) Name() string       { return "." }
func (fi *fileinfo) Size() int64        { return 0 }
func (fi *fileinfo) Mode() fs.FileMode  { return fs.ModeDevice | 0222 }
func (fi *fileinfo) ModTime() time.Time { return time.Time{} }
func (fi *fileinfo) IsDir() bool        { return false }
func (fi *fileinfo) Sys() interface{}   { return nil }
