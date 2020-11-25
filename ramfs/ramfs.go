// Copyright 2020 The Embedded Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package ramfs implements an in RAM file system.
package ramfs

import (
	"io/fs"
	"strings"
	"sync"
	"syscall"
	"time"
)

// A node represents a filesystem node
type node struct {
	name string
	lock sync.RWMutex
	data interface{} // *node for directory, []byte for file
	fs   *FS

	next, prev *node // points to other nodes in the same directory
}

// An FS represents a file system in RAM.
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

// find finds a node with path name inside directory dir
func find(dir *node, name string) *node {
	var name1 string
	if i := strings.IndexByte(name, '/'); i > 0 {
		name1 = name[i+1:]
		name = name[:i]
	}
	list := dir.data.(*node)
	list.lock.RLock()
	defer list.lock.RUnlock()
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

func open(n *node, name string, closed func(), flag int) fs.File {
	if _, ok := n.data.(*node); ok {
		return &dir{name: name, n: n, closed: closed}
	}
	return &file{name: name, n: n, closed: closed,
		rdwr: flag & (syscall.O_RDONLY | syscall.O_WRONLY | syscall.O_RDWR)}
}

func wrapErr(op, name string, err error) error {
	return &fs.PathError{Op: op, Path: name, Err: err}
}

func (fsys *FS) OpenWithFinalizer(name string, flag int, perm fs.FileMode, closed func()) (fs.File, error) {
	if !fs.ValidPath(name) {
		return nil, wrapErr("open", name, syscall.ENOENT)
	}
	if name == "." {
		if flag&syscall.O_CREAT != 0 {
			return nil, wrapErr("open", name, syscall.ENOTSUP)
		}
		return open(&fsys.root, name, closed, flag), nil
	}
	if flag&syscall.O_CREAT == 0 {
		n := find(&fsys.root, name)
		if n == nil {
			return nil, syscall.ENOENT
		}
		return open(n, name, closed, flag), nil
	}
	var dir *node
	if i := strings.LastIndexByte(name, '/'); i < 0 {
		dir = &fsys.root
	} else {
		dir = find(&fsys.root, name[:i])
		if _, ok := dir.data.(*node); !ok {
			return nil, wrapErr("open", name, syscall.ENOTDIR)
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
		return open(n, name, closed, flag), nil
	}
	if flag&syscall.O_EXCL != 0 {
		return nil, syscall.EEXIST
	}
	return open(n, name, closed, flag), nil
}

type fileinfo struct{}

func (fi *fileinfo) Name() string       { return "." }
func (fi *fileinfo) Size() int64        { return 0 }
func (fi *fileinfo) Mode() fs.FileMode  { return 0222 }
func (fi *fileinfo) ModTime() time.Time { return time.Time{} }
func (fi *fileinfo) IsDir() bool        { return false }
func (fi *fileinfo) Sys() interface{}   { return nil }
