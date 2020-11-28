// Copyright 2020 The Embedded Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ramfs

import (
	"io"
	"io/fs"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// A file represents an open file
type file struct {
	name string
	n    *node
	rdwr int

	lock   sync.Mutex // protects the fields below
	pos    int
	closed func()
}

func (f *file) Read(p []byte) (int, error) {
	if f.rdwr == syscall.O_WRONLY {
		return 0, wrapErr("read", f.name, syscall.ENOTSUP)
	}
	if f.n.isFile == nil {
		return 0, wrapErr("read", f.name, syscall.EISDIR)
	}
	f.lock.Lock()
	defer f.lock.Unlock()
	if f.closed == nil {
		return 0, wrapErr("read", f.name, syscall.EBADF)
	}
	f.n.lock.RLock()
	defer f.n.lock.RUnlock()
	if f.pos >= len(f.n.data) {
		return 0, io.EOF
	}
	n := copy(p, f.n.data[f.pos:])
	f.pos += n
	return n, nil
}

func (f *file) Write(p []byte) (int, error) {
	if f.rdwr == syscall.O_RDONLY {
		return 0, wrapErr("write", f.name, syscall.ENOTSUP)
	}
	if f.n.isFile == nil {
		return 0, wrapErr("write", f.name, syscall.EISDIR)
	}
	f.lock.Lock()
	defer f.lock.Unlock()
	if f.closed == nil {
		return 0, wrapErr("write", f.name, syscall.EBADF)
	}
	f.n.lock.Lock()
	defer f.n.lock.Unlock()
	pos1 := f.pos + len(p)
	if add := pos1 - cap(f.n.data); add > 0 {
		if atomic.AddInt64(&f.n.isFile.size, int64(add)) > f.n.isFile.maxSize {
			atomic.AddInt64(&f.n.isFile.size, int64(-add))
			return 0, wrapErr("write", f.name, syscall.ENOSPC)
		}
		data1 := make([]byte, pos1)
		copy(data1[:f.pos], f.n.data)
		f.n.data = data1
	} else {
		f.n.data = f.n.data[:pos1]
	}
	copy(f.n.data[f.pos:], p)
	f.pos = pos1
	mtime := time.Now()
	f.n.nsec = mtime.Nanosecond()
	f.n.sec = mtime.Unix()
	return len(p), nil
}

func (f *file) Stat() (fs.FileInfo, error) {
	fi := new(fileinfo)
	return fi, nil
}

func (f *file) Close() error {
	f.lock.Lock()
	defer f.lock.Unlock()
	if f.closed == nil {
		return wrapErr("close", f.name, syscall.EBADF)
	}
	f.closed()
	f.closed = nil
	return nil
}
