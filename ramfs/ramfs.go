// Copyright 2020 The Embedded Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package ramfs implements an in RAM file system.
package ramfs

import (
	"io"
	"io/fs"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

type node struct {
	name string
	lock sync.RWMutex
	data interface{} // *node for directory, []byte for file
	fs   *FS

	next, prev *node
}

type FS struct {
	size    int64
	maxSize int64
	root    node
}

func New(maxSize int64) *FS {
	fsys := new(FS)
	fsys.maxSize = maxSize
	fsys.root.name = "."
	fsys.root.next = &fsys.root
	fsys.root.prev = &fsys.root
	fsys.root.data = fsys.root
	return fsys
}

func find(dir *node, name string) *node {
	var name1 string
	if i := strings.IndexByte(name, '/'); i > 0 {
		name1 = name[i+1:]
		name = name[:i]
	}
	dir.lock.RLock()
	defer dir.lock.RUnlock()
	list := dir.data.(*node)
	for n := list.next; n != list; n = n.next {
		if n.name == name {
			if len(name1) == 0 {
				return n
			}
			if dir1, ok := n.data.(*node); ok {
				return find(dir1, name1)
			}
			break
		}
	}
	return nil
}

func (fsys *FS) OpenWithFinalizer(name string, flag int, perm fs.FileMode, closed func()) (fs.File, error) {
	if !fs.ValidPath(name) {
		return nil, syscall.ENOENT
	}
	if name == "." {
		if flag&syscall.O_CREAT != 0 {
			return nil, syscall.ENOTSUP
		}
		return open(&fsys.root, closed), nil
	}
	if flag&syscall.O_CREAT == 0 {
		n := find(&fsys.root, name)
		if n == nil {
			return nil, syscall.ENOENT
		}
		return open(n, closed), nil
	}
	var dir *node
	if i := strings.LastIndexByte(name, '/'); i < 0 {
		dir = &fsys.root
	} else {
		dir = find(&fsys.root, name[:i])
		if _, ok := dir.data.(*node); !ok {
			return nil, syscall.ENOTDIR
		}
		name = name[i+1:]
	}
	n := find(dir, name)
	if n == nil {
		list := dir.data.(*node)
		n := &node{name: name, data: []byte{}, prev: list}
		list.lock.Lock()
		n.next = list.next
		list.next.prev = n
		list.next = n
		list.lock.Unlock()
		return open(n, closed), nil
	}
	if flag&syscall.O_EXCL != 0 {
		return nil, syscall.EEXIST
	}

	return nil, nil
}

func open(n *node, closed func()) fs.File {
	if _, ok := n.data.(*node); ok {
		return &dir{n, 0, closed}
	}
	return &file{n, 0, closed}
}

// A file represents an open file
type file struct {
	n      *node
	pos    int // use int instead of int64 for better speed on 32-bit arch
	closed func()
}

func (f *file) Read(p []byte) (int, error) {
	f.n.lock.RLock()
	defer f.n.lock.RUnlock()
	data, ok := f.n.data.([]byte)
	if !ok {
		return 0, syscall.EISDIR
	}
	if f.pos >= len(data) {
		return 0, io.EOF
	}
	n := copy(p, data[f.pos:])
	f.pos += n
	return n, nil
}

func (f *file) Write(p []byte) (int, error) {
	f.n.lock.Lock()
	defer f.n.lock.Unlock()
	data, ok := f.n.data.([]byte)
	if !ok {
		return 0, syscall.EISDIR
	}
	pos1 := f.pos + len(p)
	if add := pos1 - cap(data); add > 0 {
		if atomic.AddInt64(&f.n.fs.size, int64(add)) > f.n.fs.maxSize {
			atomic.AddInt64(&f.n.fs.size, int64(-add))
			return 0, syscall.ENOSPC
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

// A dir represents an open directory
type dir struct {
	n      *node
	pos    int
	closed func()
}

func (d *dir) Read(p []byte) (int, error) {
	return 0, syscall.ENOTSUP
}

func (d *dir) Stat() (fs.FileInfo, error) {
	return nil, syscall.ENOTSUP
}

func (d *dir) ReadDir(n int) ([]fileinfo, error) {
	return nil, syscall.ENOTSUP
}

func (d *dir) Close() error {
	d.closed()
	return nil
}

type fileinfo struct{}

func (fi *fileinfo) Name() string       { return "." }
func (fi *fileinfo) Size() int64        { return 0 }
func (fi *fileinfo) Mode() fs.FileMode  { return 0222 }
func (fi *fileinfo) ModTime() time.Time { return time.Time{} }
func (fi *fileinfo) IsDir() bool        { return false }
func (fi *fileinfo) Sys() interface{}   { return nil }
