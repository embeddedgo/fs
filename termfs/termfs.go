// Copyright 2020 The Embedded Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package termfs provides lightwait implementation of terminal device
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
	r         io.Reader
	w         io.Writer
	name      string
	rlock     sync.Mutex
	wlock     sync.Mutex
	replaceLF []byte
}

// New returns a new file system named name. The r and w are used respectively
// to read from and write to the terminal device. If the replaceLF string is not
// empty it is used to replace the new line character '\n' in output data.
func New(name string, r io.Reader, w io.Writer, replaceLF string) *FS {
	fsys := &FS{r: r, w: w, name: name}
	if len(replaceLF) != 0 && replaceLF != "\n" {
		fsys.replaceLF = []byte(replaceLF)
	}
	return fsys
}

// OpenWithFinalizer implements the rtos.FS OpenWithFinalizer method. The name
// must be ".", the flag can be O_RDWR, O_RDONLY, O_WRONLY, the perm is ignored.
func (fsys *FS) OpenWithFinalizer(name string, flag int, perm fs.FileMode, closed func()) (fs.File, error) {
	if name != "." {
		return nil, &fs.PathError{Op: "open", Path: name, Err: syscall.ENOENT}
	}
	if flag&(syscall.O_RDONLY|syscall.O_WRONLY|syscall.O_RDWR) != 0 {
		return nil, &fs.PathError{Op: "open", Path: name, Err: syscall.EINVAL}
	}
	return &file{fsys, flag, closed}, nil
}

// Open implements the fs.FS Open method.
func (fsys *FS) Open(name string) (fs.File, error) {
	return fsys.OpenWithFinalizer(name, syscall.O_RDONLY, 0, nil)
}

// Type implements the rtos.FS Type method
func (fsys *FS) Type() string { return "term" }

// Name implements the rtos.FS Name method
func (fsys *FS) Name() string { return fsys.name }

type file struct {
	fs     *FS
	flag   int
	closed func()
}

func wrapErr(op string, err error) error {
	if err != nil {
		return &fs.PathError{Op: op, Path: ".", Err: err}
	}
	return nil
}

func (f *file) Read(p []byte) (int, error) {
	if f.flag == syscall.O_WRONLY {
		return 0, wrapErr("read", syscall.ENOTSUP)
	}
	if len(p) == 0 {
		return 0, nil
	}
	f.fs.rlock.Lock()
	defer f.fs.rlock.Unlock()
	if f.closed == nil {
		return 0, wrapErr("read", syscall.EBADF)
	}
	n, err := f.fs.r.Read(p)
	return n, wrapErr("read", err)
}

func (f *file) Write(p []byte) (int, error) {
	if f.flag == syscall.O_RDONLY {
		return 0, wrapErr("write", syscall.ENOTSUP)
	}
	if len(p) == 0 {
		return 0, nil
	}
	f.fs.wlock.Lock()
	defer f.fs.wlock.Unlock()
	if f.closed == nil {
		return 0, wrapErr("write", syscall.EBADF)
	}
	if len(f.fs.replaceLF) == 0 {
		n, err := f.fs.w.Write(p)
		err = wrapErr("write", err)
		return n, err
	}
	n := 0
	for {
		m := n
		for p[m] != '\n' {
			m++
		}
		if m != n {
			m, err := f.fs.w.Write(p[n:m])
			n += m
			if err != nil {
				return n, wrapErr("write", err)
			}
		}
		if n >= len(p) {
			return n, nil
		}
		_, err := f.fs.w.Write(f.fs.replaceLF[:])
		if n++; n >= len(p) {
			return n, wrapErr("write", err)
		}
	}
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
