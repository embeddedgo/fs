// Copyright 2020 The Embedded Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package ramfs implements an in RAM file system.
package ramfs

import (
	"io/fs"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// A node represents a filesystem node
type node struct {
	name string
	fs   *FS

	lock       sync.RWMutex
	data       interface{} // *node for directory, []byte for file
	next, prev *node       // points to other nodes in the same directory
}

const emptyNodeSize = (2 + 1 + 3 + 2 + 2) * 8 // approximate empty node size

func (n *node) size() int64 {
	var size int
	if data, ok := n.data.([]byte); ok {
		size = emptyNodeSize + cap(data)
	} else {
		size = 2 * emptyNodeSize
	}
	return int64(size)
}

// An FS represents a file system in RAM.
type FS struct {
	size    int64
	maxSize int64
	root    node
	items   int32
}

func New(maxSize int64) *FS {
	fsys := new(FS)
	fsys.maxSize = maxSize
	fsys.root.name = "."
	fsys.root.next = &fsys.root
	fsys.root.prev = &fsys.root
	fsys.root.data = &fsys.root
	return fsys
}

// find searches the tree starting from root directory for a node with a given
// path name.
func find(root *node, name string) *node {
	var name1 string
	if i := strings.IndexByte(name, '/'); i > 0 {
		name1 = name[i+1:]
		name = name[:i]
	}
	list := root.data.(*node)
	list.lock.RLock()
	defer list.lock.RUnlock()
	for n := list.next; n != list; n = n.next {
		if n.name == name {
			if len(name1) == 0 {
				return n
			}
			if root1, ok := n.data.(*node); ok {
				return find(root1, name1)
			}
			break
		}
	}
	return nil
}

// findDir works like path.Split but also searches for a directory starting from
// root directory and returns the corresponding node if found.
func findDir(root *node, name string) (dir *node, base string) {
	i := strings.LastIndexByte(name, '/')
	if i < 0 {
		return root, name
	}
	dir = find(root, name[:i])
	if dir == nil {
		return nil, ""
	}
	if _, ok := dir.data.(*node); !ok {
		return nil, name[:i]
	}
	return dir, name[i+1:]
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

// OpenWithFinalizer implements the rtos.FS OpenWithFinalizer method.
func (fsys *FS) OpenWithFinalizer(name string, flag int, perm fs.FileMode, closed func()) (fs.File, error) {
	if !fs.ValidPath(name) {
		return nil, wrapErr("open", name, syscall.EINVAL)
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
	dir, base := findDir(&fsys.root, name)
	if dir == nil {
		if base != "" {
			return nil, wrapErr("open", base, syscall.ENOTDIR)
		}
		return nil, wrapErr("open", name, syscall.ENOENT)
	}
	n := find(dir, base)
	if n == nil {
		if atomic.AddInt64(&fsys.size, emptyNodeSize) > fsys.maxSize {
			atomic.AddInt64(&fsys.size, -emptyNodeSize)
			return nil, wrapErr("open", name, syscall.ENOSPC)
		}
		atomic.AddInt32(&fsys.items, 1)
		list := dir.data.(*node)
		n := &node{name: name, fs: fsys, data: []byte{}, prev: list}
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

func nop() {}

// Open implements the fs.FS Open method.
func (fsys *FS) Open(name string) (fs.File, error) {
	return fsys.OpenWithFinalizer(name, 0, 0, nop)
}

func (fsys *FS) Mkdir(name string) error {
	return syscall.ENOTSUP
}

// Usage implements the rtos.UsageFS Usage method.
func (fsys *FS) Usage() (usedItems, maxItems int, usedBytes, maxBytes int64) {
	return int(atomic.LoadInt32(&fsys.items)), -1,
		atomic.LoadInt64(&fsys.size), fsys.maxSize
}

func (fsys *FS) Remove(name string) error {
	if !fs.ValidPath(name) {
		return wrapErr("remove", name, syscall.EINVAL)
	}
	if name == "." {
		return wrapErr("remove", name, syscall.ENOTSUP)
	}
	dir, base := findDir(&fsys.root, name)
	if dir == nil {
		return wrapErr("remove", name, syscall.ENOENT)
	}
	list := dir.data.(*node)
	list.lock.Lock()
	defer list.lock.Unlock()
	for n := list.next; n != list; n = n.next {
		if n.name == base {
			n.prev.next = n.next
			n.next.prev = n.prev
			atomic.AddInt32(&fsys.items, -1)
			atomic.AddInt64(&fsys.size, -n.size())
			return nil
		}
	}
	return wrapErr("remove", name, syscall.ENOENT)
}

func (fsys *FS) Rename(oldname, newname string) error {
	olddir, oldbase := findDir(&fsys.root, oldname)
	if olddir == nil {
		return wrapErr("rename", oldname, syscall.ENOENT)
	}
	newdir, newbase := findDir(&fsys.root, newname)
	if newdir == nil {
		return wrapErr("rename", newname, syscall.ENOENT)
	}
	list := olddir.data.(*node)
	list.lock.Lock()
	n := list.next
	for n != list {
		if n.name == oldbase {
			n.prev.next = n.next
			n.next.prev = n.prev
			break
		}
		n = n.next
	}
	list.lock.Unlock()
	if n == list {
		return wrapErr("rename", oldname, syscall.ENOENT)
	}
	list = newdir.data.(*node)
	n.name = newbase
	n.prev = list
	list.lock.Lock()
	n.next = list.next
	list.next.prev = n
	list.next = n
	list.lock.Unlock()
	return nil
}

type fileinfo struct{}

func (fi *fileinfo) Name() string       { return "." }
func (fi *fileinfo) Size() int64        { return 0 }
func (fi *fileinfo) Mode() fs.FileMode  { return 0222 }
func (fi *fileinfo) ModTime() time.Time { return time.Time{} }
func (fi *fileinfo) IsDir() bool        { return false }
func (fi *fileinfo) Sys() interface{}   { return nil }
