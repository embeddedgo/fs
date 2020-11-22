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
// to read from and write to the terminal device. The replaceLF string if not
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
		return nil, syscall.ENOENT
	}
	switch flag {
	case syscall.O_RDWR, syscall.O_RDONLY, syscall.O_WRONLY:
		return &file{fsys, flag, closed}, nil
	default:
		return nil, syscall.EINVAL
	}
}

// Open implements the fs.FS Open method.
func (fsys *FS) Open(name string) (fs.File, error) {
	f, err := fsys.OpenWithFinalizer(name, syscall.O_RDONLY, 0, nil)
	if err != nil {
		return nil, &fs.PathError{Op: "open", Path: name, Err: err}
	}
	return f, nil
}

// Type implements the rtos.FS Type method
func (fsys *FS) Type() string { return "term" }

// Name implements the rtos.FS Name method
func (fsys *FS) Name() string { return fsys.name }

// Sync implements the rtos.FS Sync method.
func (fsys *FS) Sync() error { return nil }

type file struct {
	fs     *FS
	flag   int
	closed func()
}

func (f *file) Read(p []byte) (int, error) {
	if f.flag == syscall.O_WRONLY {
		return 0, syscall.ENOTSUP
	}
	if len(p) == 0 {
		return 0, nil
	}
	f.fs.rlock.Lock()
	defer f.fs.rlock.Unlock()
	if f.closed == nil {
		return 0, syscall.EINVAL
	}
	return f.fs.r.Read(p)
}

func (f *file) Write(p []byte) (int, error) {
	if f.flag == syscall.O_RDONLY {
		return 0, syscall.ENOTSUP
	}
	if len(p) == 0 {
		return 0, nil
	}
	f.fs.wlock.Lock()
	defer f.fs.wlock.Unlock()
	if f.closed == nil {
		return 0, syscall.EINVAL
	}
	if len(f.fs.replaceLF) == 0 {
		return f.fs.w.Write(p)
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
				return n, err
			}
		}
		if n >= len(p) {
			return n, nil
		}
		_, err := f.fs.w.Write(f.fs.replaceLF[:])
		if n++; n >= len(p) {
			return n, err
		}
	}
}

func (f *file) Stat() (fs.FileInfo, error) {
	return &fileinfo{}, nil
}

func (f *file) Close() error {
	f.fs.rlock.Lock()
	f.fs.wlock.Lock()
	defer f.fs.wlock.Unlock()
	defer f.fs.rlock.Unlock()
	if f.closed == nil {
		return syscall.EINVAL
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