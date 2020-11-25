// Copyright 2020 The Embedded Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ramfs

import (
	"io"
	"io/fs"
	"sync/atomic"
	"syscall"
)

// A file represents an open file
type file struct {
	name   string
	n      *node
	pos    int // use int instead of int64 for better speed on 32-bit arch
	closed func()
	rdwr   int
}

func (f *file) Read(p []byte) (int, error) {
	if f.rdwr == syscall.O_WRONLY {
		return 0, wrapErr(f.name, "read", syscall.ENOTSUP)
	}
	f.n.lock.RLock()
	defer f.n.lock.RUnlock()
	data, ok := f.n.data.([]byte)
	if !ok {
		return 0, wrapErr(f.name, "read", syscall.EISDIR)
	}
	if f.pos >= len(data) {
		return 0, io.EOF
	}
	n := copy(p, data[f.pos:])
	f.pos += n
	return n, nil
}

func (f *file) Write(p []byte) (int, error) {
	if f.rdwr == syscall.O_RDONLY {
		return 0, wrapErr(f.name, "write", syscall.ENOTSUP)
	}
	f.n.lock.Lock()
	defer f.n.lock.Unlock()
	data, ok := f.n.data.([]byte)
	if !ok {
		return 0, wrapErr(f.name, "write", syscall.EISDIR)
	}
	pos1 := f.pos + len(p)
	if add := pos1 - cap(data); add > 0 {
		if atomic.AddInt64(&f.n.fs.size, int64(add)) > f.n.fs.maxSize {
			atomic.AddInt64(&f.n.fs.size, int64(-add))
			return 0, wrapErr(f.name, "write", syscall.ENOSPC)
		}
		data1 := make([]byte, pos1)
		copy(data1[:f.pos], data)
		data = data1
	} else {
		data = data[:pos1]
	}
	copy(data[f.pos:], p)
	f.n.data = data
	f.pos = pos1
	return len(p), nil
}

func (f *file) Stat() (fs.FileInfo, error) {
	return &fileinfo{}, nil
}

func (f *file) Close() error {
	f.closed()
	return nil
}
