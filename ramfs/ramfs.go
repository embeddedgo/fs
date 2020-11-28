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
	isFile *FS // non-nil for file, nil for directory

	// the following three fields are protected by lock in the parent node
	name string
	next *node // points to the next node in the same directory

	lock sync.RWMutex // protects the following fields
	list *node
	data []byte
	sec  int64
	nsec int
}

const (
	msbit = ^uintptr(0) - ^uintptr(0)>>1

	logPtrSize = 2*(msbit>>31&1) + 3*(msbit>>63&1) + 4*(msbit>>127&1)
	ptrSize    = 1 << logPtrSize

	intSize   = ptrSize
	strSize   = 2 * ptrSize
	sliSize   = 3 * ptrSize
	lockSize  = 6 * 4
	intrfSize = 2 * ptrSize

	nodeSize = ptrSize + strSize + ptrSize + lockSize + ptrSize + sliSize + 8 + intSize

	emptyFileSize = nodeSize
	dirSize       = nodeSize
)

func (n *node) size() int64 {
	size := dirSize
	if n.isFile != nil {
		n.lock.RLock()
		size = emptyFileSize + cap(n.data)
		n.lock.RUnlock()
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
	root.lock.RLock()
	defer root.lock.RUnlock()
	for n := root.list; n != nil; n = n.next {
		if n.name == name {
			if len(name1) == 0 {
				return n
			}
			if n.isFile == nil {
				return find(n, name1)
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
	if dir == nil || dir.isFile != nil {
		return dir, name[:i]
	}
	return dir, name[i+1:]
}

func open(n *node, name string, closed func(), flag int) fs.File {
	if n.isFile == nil {
		return &dir{name: name, n: n, closed: closed}
	}
	return &file{name: name, n: n, closed: closed,
		rdwr: flag & (syscall.O_RDONLY | syscall.O_WRONLY | syscall.O_RDWR)}
}

func wrapErr(op, name string, err error) error {
	return &fs.PathError{Op: op, Path: name, Err: err}
}

// OpenWithFinalizer implements the rtos.FS OpenWithFinalizer method.
func (fsys *FS) OpenWithFinalizer(name string, flag int, _ fs.FileMode, closed func()) (fs.File, error) {
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
		return nil, wrapErr("open", base, syscall.ENOENT)
	}
	if dir.isFile != nil {
		return nil, wrapErr("open", base, syscall.ENOTDIR)
	}
	n := find(dir, base)
	if n == nil {
		if atomic.AddInt64(&fsys.size, emptyFileSize) > fsys.maxSize {
			atomic.AddInt64(&fsys.size, -emptyFileSize)
			return nil, wrapErr("open", name, syscall.ENOSPC)
		}
		atomic.AddInt32(&fsys.items, 1)
		mtime := time.Now()
		n := &node{
			isFile: fsys,
			name:   base,
			sec:    mtime.Unix(),
			nsec:   mtime.Nanosecond(),
		}
		dir.lock.Lock()
		n.next = dir.list
		dir.list = n
		dir.sec = n.sec
		dir.nsec = n.nsec
		dir.lock.Unlock()
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

// Mkdir creates a directory with a given name.
func (fsys *FS) Mkdir(name string, _ fs.FileMode) error {
	if !fs.ValidPath(name) {
		return wrapErr("mkdir", name, syscall.EINVAL)
	}
	if name == "." {
		return wrapErr("mkdir", name, syscall.EEXIST)
	}
	dir, base := findDir(&fsys.root, name)
	if dir == nil {
		return wrapErr("mkdir", base, syscall.ENOENT)
	}
	if dir.isFile != nil {
		return wrapErr("mkdir", base, syscall.ENOTDIR)
	}
	if atomic.AddInt64(&fsys.size, dirSize) > fsys.maxSize {
		atomic.AddInt64(&fsys.size, -dirSize)
		return wrapErr("mkdir", name, syscall.ENOSPC)
	}
	atomic.AddInt32(&fsys.items, 1)
	mtime := time.Now()
	n := &node{
		name: base,
		sec:  mtime.Unix(),
		nsec: mtime.Nanosecond(),
	}
	dir.lock.Lock()
	n.next = dir.list
	dir.list = n
	dir.sec = n.sec
	dir.nsec = n.nsec
	dir.lock.Unlock()
	return nil
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
	dir.lock.Lock()
	n := dir.list
	if n != nil {
		if n.name == base {
			dir.list = n.next
		} else {
			for {
				prev := n
				n = n.next
				if n == nil {
					break
				}
				if n.name == base {
					prev.next = n.next
					break
				}
			}
		}
	}
	dir.lock.Unlock()
	if n == nil {
		return wrapErr("remove", name, syscall.ENOENT)
	}
	atomic.AddInt32(&fsys.items, -1)
	atomic.AddInt64(&fsys.size, -n.size())
	return nil
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

type fileinfo struct {
	msec  int64
	mnsec int
	name  string
	isdir bool
}

func (fi *fileinfo) Name() string       { return "." }
func (fi *fileinfo) Size() int64        { return 0 }
func (fi *fileinfo) Mode() fs.FileMode  { return 0222 }
func (fi *fileinfo) ModTime() time.Time { return time.Time{} }
func (fi *fileinfo) IsDir() bool        { return false }
func (fi *fileinfo) Sys() interface{}   { return nil }
