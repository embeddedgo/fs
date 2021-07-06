// Copyright 2020 The Embedded Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package termfs

import (
	"io"
	"io/fs"
	"sync"
	"syscall"
)

// An LightFS provides a file system that represents a terminal device. It is
// a thin wrapper over provided io.Reader and io.Writer that allows use them
// concurently by multiple goroutines. Unlike FS it does not support CR/LF
// conversions, echo, line editing. If you need such futures use FS or configure
// your terminal emulator to handle them locally.
type LightFS struct {
	r    io.Reader
	w    io.Writer
	name string
	rmu  sync.Mutex
	wmu  sync.Mutex
}

// NewLight returns a new terminal file system named name. The r and w
// correspond to the terminal input and output device.
func NewLight(name string, r io.Reader, w io.Writer) *LightFS {
	return &LightFS{r: r, w: w, name: name}
}

// OpenWithFinalizer implements the rtos.FS OpenWithFinalizer method. The name
// must be ".". The flag and perm are ignored.
func (fsys *LightFS) OpenWithFinalizer(name string, flag int, perm fs.FileMode, closed func()) (fs.File, error) {
	if name != "." {
		return nil, &fs.PathError{Op: "open", Path: name, Err: syscall.ENOENT}
	}
	return &lightFile{fsys, closed}, nil
}

// Type implements the rtos.FS Type method
func (fsys *LightFS) Type() string { return "lterm" }

// Name implements the rtos.FS Name method
func (fsys *LightFS) Name() string { return fsys.name }

// Usage implements the rtos.FS Usage method
func (fsys *LightFS) Usage() (int, int, int64, int64) { return -1, -1, -1, -1 }

type lightFile struct {
	fs     *LightFS
	closed func()
}

func (f *lightFile) Read(p []byte) (n int, err error) {
	if len(p) == 0 {
		return 0, nil
	}
	f.fs.rmu.Lock()
	n, err = f.fs.r.Read(p[:n])
	if err != nil && err != io.EOF {
		err = wrapErr("read", err)
	}
	f.fs.rmu.Unlock()
	return n, err
}

func (f *lightFile) Write(p []byte) (n int, err error) {
	if len(p) == 0 {
		return 0, nil
	}
	f.fs.wmu.Lock()
	n, err = f.fs.w.Write(p[:n])
	if err != nil {
		err = wrapErr("write", err)
	}
	f.fs.wmu.Unlock()
	return n, err
}

func (f *lightFile) Stat() (fs.FileInfo, error) {
	return &fileinfo{}, nil
}

func (f *lightFile) Close() (err error) {
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
