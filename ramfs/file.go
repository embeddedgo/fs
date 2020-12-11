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
	rdwr int

	mu     sync.Mutex // protects the fields below
	n      *node
	pos    int
	closed func()
}

func (f *file) Read(p []byte) (n int, err error) {
	if f.rdwr == syscall.O_WRONLY {
		err = syscall.EBADF
		goto end
	}
	f.mu.Lock()
	if f.n == nil {
		err = syscall.EBADF
	} else if f.n.fileFS == nil {
		err = syscall.EISDIR
	} else {
		f.n.mu.RLock()
		if f.pos < len(f.n.data) {
			n = copy(p, f.n.data[f.pos:])
			f.pos += n
		} else {
			err = io.EOF
		}
		f.n.mu.RUnlock()
	}
	f.mu.Unlock()
end:
	if err != nil && err != io.EOF {
		err = wrapErr("read", f.name, err)
	}
	return n, err
}

func (f *file) Write(p []byte) (n int, err error) {
	if f.rdwr == syscall.O_RDONLY {
		err = syscall.EBADF
		goto end
	}
	f.mu.Lock()
	if f.n == nil {
		err = syscall.EBADF
	} else if f.n.fileFS == nil {
		err = syscall.EISDIR
	} else {
		f.n.mu.Lock()
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
			if atomic.AddInt64(&f.n.fileFS.size, int64(add)) > f.n.fileFS.maxSize {
				atomic.AddInt64(&f.n.fileFS.size, int64(-add))
				err = syscall.ENOSPC
				goto skip
			}
			data1 := make([]byte, pos1, newCap)
			copy(data1[:f.pos], f.n.data)
			f.n.data = data1
		} else if pos1 > len(f.n.data) {
			f.n.data = f.n.data[:pos1]
		}
		copy(f.n.data[f.pos:], p)
		f.pos = pos1
		{
			mtime := time.Now()
			f.n.modSec = mtime.Unix()
			f.n.modNsec = mtime.Nanosecond()
		}
		n = len(p)
	skip:
		f.n.mu.Unlock()
	}
	f.mu.Unlock()
end:
	if err != nil {
		err = wrapErr("write", f.name, err)
	}
	return n, err
}

func (f *file) Stat() (fs.FileInfo, error) {
	f.mu.Lock()
	fi := stat(f.n)
	f.mu.Unlock()
	return fi, nil
}

func (f *file) Close() error {
	var err error
	f.mu.Lock()
	if f.n == nil {
		err = wrapErr("close", f.name, syscall.EBADF)
	} else {
		f.closed()
		f.closed = nil
		f.n = nil
	}
	f.mu.Unlock()
	return err
}
