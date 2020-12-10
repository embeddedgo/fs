// Copyright 2020 The Embedded Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ramfs

import (
	"io"
	"io/fs"
	"path"
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

	mu     sync.Mutex // protects the fields below
	pos    int
	closed func()
}

func (f *file) Read(p []byte) (n int, err error) {
	if f.rdwr == syscall.O_WRONLY {
		return 0, wrapErr("read", f.name, syscall.ENOTSUP)
	}
	if f.n.isFile == nil {
		return 0, wrapErr("read", f.name, syscall.EISDIR)
	}
	f.mu.Lock()
	if f.closed != nil {
		f.n.mu.RLock()
		if f.pos < len(f.n.data) {
			n = copy(p, f.n.data[f.pos:])
			f.pos += n
		} else {
			err = io.EOF
		}
		f.n.mu.RUnlock()
	} else {
		err = wrapErr("read", f.name, syscall.EBADF)
	}
	f.mu.Unlock()
	return
}

func (f *file) Write(p []byte) (n int, err error) {
	if f.rdwr == syscall.O_RDONLY {
		return 0, wrapErr("write", f.name, syscall.ENOTSUP)
	}
	if f.n.isFile == nil {
		return 0, wrapErr("write", f.name, syscall.EISDIR)
	}
	f.mu.Lock()
	if f.closed == nil {
		err = syscall.EBADF
		goto end
	}
	f.n.mu.Lock()
	for {
		pos1 := f.pos + len(p)
		if pos1 > cap(f.n.data) {
			var roundUp int
			switch {
			case cap(f.n.data) < 64:
				roundUp = 15
			case cap(f.n.data) < 256:
				roundUp = 31
			default:
				roundUp = 63
			}
			newCap := (pos1 + roundUp) &^ roundUp
			add := newCap - cap(f.n.data)
			if atomic.AddInt64(&f.n.isFile.size, int64(add)) > f.n.isFile.maxSize {
				atomic.AddInt64(&f.n.isFile.size, int64(-add))
				err = syscall.ENOSPC
				break
			}
			data1 := make([]byte, pos1, newCap)
			copy(data1[:f.pos], f.n.data)
			f.n.data = data1
		} else if pos1 > len(f.n.data) {
			f.n.data = f.n.data[:pos1]
		}
		copy(f.n.data[f.pos:], p)
		f.pos = pos1
		mtime := time.Now()
		f.n.modSec = mtime.Unix()
		f.n.modNsec = mtime.Nanosecond()
		n = len(p)
		break
	}
	f.n.mu.Unlock()
end:
	f.mu.Unlock()
	if err != nil {
		err = wrapErr("write", f.name, err)
	}
	return
}

func (f *file) Stat() (fs.FileInfo, error) {
	info := &fileInfo{
		name:  path.Base(f.name),
		isDir: f.n.isFile == nil,
	}
	f.n.mu.RLock()
	info.modSec = f.n.modSec
	info.modNsec = f.n.modNsec
	info.size = len(f.n.data)
	f.n.mu.RUnlock()
	return info, nil
}

func (f *file) Close() error {
	var err error
	f.mu.Lock()
	if f.closed != nil {
		f.closed()
		f.closed = nil
	} else {
		err = wrapErr("close", f.name, syscall.EBADF)
	}
	f.mu.Unlock()
	return err
}
