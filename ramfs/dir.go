// Copyright 2020 The Embedded Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ramfs

import (
	"io"
	"io/fs"
	"sync"
	"syscall"
)

// A dir represents an open directory
type dir struct {
	name string

	mu     sync.Mutex // protects the fields below
	n      *node
	pos    int
	closed func()
}

func (d *dir) Read(p []byte) (int, error) {
	return 0, syscall.ENOTSUP
}

func (d *dir) Stat() (fs.FileInfo, error) {
	d.mu.Lock()
	fi := stat(d.n)
	d.mu.Unlock()
	return fi, nil
}

func (d *dir) ReadDir(n int) (de []fs.DirEntry, err error) {
	d.mu.Lock()
	d.n.mu.RLock()
	var first *node
	m := 0
	for e := d.n.list; e != nil; e = e.next {
		if m == d.pos {
			first = e
		}
		m++
	}
	m -= d.pos
	if m == 0 {
		err = io.EOF
	} else {
		if n > 0 && m > n {
			m = n
		}
		d.pos += m
		de = make([]fs.DirEntry, m)
		for i := range de {
			de[i] = stat(first)
			first = first.next
		}
	}
	d.n.mu.RUnlock()
	d.mu.Unlock()
	return de, err
}

func (d *dir) Close() error {
	var err error
	d.mu.Lock()
	if d.n == nil {
		err = wrapErr("close", d.name, syscall.EBADF)
	} else {
		d.closed()
		d.closed = nil
		d.n = nil
	}
	d.mu.Unlock()
	return err
}
