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
	fileFS *FS // non-nil for file, nil for directory

	// the following three fields are protected by mx in the parent node
	name string
	next *node // points to the next node in the same directory

	mu      sync.RWMutex // protects the following fields
	list    *node
	data    []byte
	modSec  int64
	modNsec int
}

const (
	msbit      = ^uintptr(0) - ^uintptr(0)>>1
	logPtrSize = 2*(msbit>>31&1) + 3*(msbit>>63&1) + 4*(msbit>>127&1)

	ptrSize  = 1 << logPtrSize
	intSize  = ptrSize
	strSize  = 2 * ptrSize
	sliSize  = 3 * ptrSize
	lockSize = 6 * 4

	nodeSize = ptrSize + strSize + ptrSize + lockSize + ptrSize + sliSize + 8 + intSize

	emptyFileSize = nodeSize
	dirSize       = nodeSize
)

func size(n *node) int64 {
	size := dirSize
	if n.fileFS != nil {
		n.mu.RLock()
		size = emptyFileSize + cap(n.data)
		n.mu.RUnlock()
	}
	return int64(size)
}

func stat(n *node) *fileInfo {
	fi := new(fileInfo)
	fi.name = n.name
	n.mu.RLock()
	fi.isDir = n.fileFS == nil
	fi.modSec = n.modSec
	fi.modNsec = n.modNsec
	fi.size = len(n.data)
	n.mu.RUnlock()
	return fi
}

// An FS represents a file system in RAM.
type FS struct {
	size    int64
	maxSize int64
	root    node
	items   int32
	name    string
}

func New(name string, maxSize int64) *FS {
	fsys := new(FS)
	fsys.maxSize = maxSize
	fsys.name = name
	fsys.root.name = "."
	ctime := time.Now()
	fsys.root.modSec = ctime.Unix()
	fsys.root.modNsec = ctime.Nanosecond()
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
	root.mu.RLock()
	n := root.list
	for n != nil {
		if n.name == name {
			if len(name1) == 0 {
				break
			}
			if n.fileFS == nil {
				n = find(n, name1)
				break
			}
			n = nil
			break
		}
		n = n.next
	}
	root.mu.RUnlock()
	return n
}

// findDir works like path.Split but also searches for a directory starting from
// root directory and returns the corresponding node if found.
func findDir(root *node, name string) (dir *node, base string) {
	i := strings.LastIndexByte(name, '/')
	if i < 0 {
		return root, name
	}
	dir = find(root, name[:i])
	if dir == nil || dir.fileFS != nil {
		return dir, name[:i] // return the directory name
	}
	return dir, name[i+1:]
}

func open(n *node, name string, closed func(), flag, pos int) fs.File {
	if n.fileFS == nil {
		return &dir{name: name, n: n, closed: closed}
	}
	return &file{name: name, n: n, pos: pos, closed: closed,
		rdwr: flag & (syscall.O_RDONLY | syscall.O_WRONLY | syscall.O_RDWR)}
}

// OpenWithFinalizer implements the rtos.FS OpenWithFinalizer method.
func (fsys *FS) OpenWithFinalizer(name string, flag int, _ fs.FileMode, closed func()) (fs.File, error) {
	var err error
	{
		if !fs.ValidPath(name) {
			err = syscall.EINVAL
			goto error
		}
		if name == "." {
			if flag&syscall.O_CREAT != 0 {
				err = syscall.ENOTSUP
				goto error
			}
			return open(&fsys.root, name, closed, flag, 0), nil
		}
		if n := find(&fsys.root, name); n != nil {
			pos := 0
			if flag&(syscall.O_TRUNC|syscall.O_APPEND) != 0 {
				n.mu.Lock()
				if flag&syscall.O_TRUNC != 0 {
					n.data = nil
				} else {
					pos = len(n.data)
				}
				n.mu.Unlock()
			}
			return open(n, name, closed, flag, pos), nil
		}
		if flag&syscall.O_CREAT == 0 {
			err = syscall.ENOENT
			goto error
		}
		dir, base := findDir(&fsys.root, name)
		if dir == nil {
			name = base
			err = syscall.ENOENT
			goto error
		}
		if dir.fileFS != nil {
			name = base
			err = syscall.ENOTDIR
			goto error
		}
		n := find(dir, base)
		if n == nil {
			if atomic.AddInt64(&fsys.size, emptyFileSize) > fsys.maxSize {
				atomic.AddInt64(&fsys.size, -emptyFileSize)
				err = syscall.ENOSPC
				goto error
			}
			atomic.AddInt32(&fsys.items, 1)
			mtime := time.Now()
			n := &node{
				fileFS:  fsys,
				name:    base,
				modSec:  mtime.Unix(),
				modNsec: mtime.Nanosecond(),
			}
			dir.mu.Lock()
			n.next = dir.list
			dir.list = n
			dir.modSec = n.modSec
			dir.modNsec = n.modNsec
			dir.mu.Unlock()
			return open(n, name, closed, flag, 0), nil
		}
		if flag&syscall.O_EXCL == 0 {
			return open(n, name, closed, flag, 0), nil
		}
		err = syscall.EEXIST
	}
error:
	closed()
	return nil, &fs.PathError{Op: "open", Path: name, Err: err}
}

func nop() {}

// Open implements the fs.FS Open method.
func (fsys *FS) Open(name string) (fs.File, error) {
	return fsys.OpenWithFinalizer(name, 0, 0, nop)
}

// Type implements the rtos.FS Type method.
func (fsys *FS) Type() string { return "ram" }

// Name implements the rtos.FS Name method.
func (fsys *FS) Name() string { return fsys.name }

// Mkdir creates a directory with a given name.
func (fsys *FS) Mkdir(name string, _ fs.FileMode) error {
	var err error
	{
		if !fs.ValidPath(name) {
			err = syscall.EINVAL
			goto error
		}
		if name == "." {
			err = syscall.EEXIST
			goto error
		}
		dir, base := findDir(&fsys.root, name)
		if dir == nil {
			name = base
			err = syscall.ENOENT
			goto error
		}
		if dir.fileFS != nil {
			name = base
			err = syscall.ENOTDIR
			goto error
		}
		if atomic.AddInt64(&fsys.size, dirSize) > fsys.maxSize {
			atomic.AddInt64(&fsys.size, -dirSize)
			err = syscall.ENOSPC
			goto error
		}
		atomic.AddInt32(&fsys.items, 1)
		mtime := time.Now()
		n := &node{
			name:    base,
			modSec:  mtime.Unix(),
			modNsec: mtime.Nanosecond(),
		}
		// BUG: check does dir exist
		dir.mu.Lock()
		n.next = dir.list
		dir.list = n
		dir.modSec = n.modSec
		dir.modNsec = n.modNsec
		dir.mu.Unlock()
		return nil
	}
error:
	return &fs.PathError{Op: "mkdir", Path: name, Err: err}
}

// Usage implements the rtos.UsageFS Usage method.
func (fsys *FS) Usage() (usedItems, maxItems int, usedBytes, maxBytes int64) {
	return int(atomic.LoadInt32(&fsys.items)), -1,
		atomic.LoadInt64(&fsys.size), fsys.maxSize
}

func unlink(dir *node, name string) *node {
	dir.mu.Lock()
	n := dir.list
	if n != nil {
		if n.name == name {
			dir.list = n.next
		} else {
			for {
				prev := n
				n = n.next
				if n == nil {
					break
				}
				if n.name == name {
					prev.next = n.next
					break
				}
			}
		}
		if n != nil {
			mtime := time.Now()
			dir.modSec = mtime.Unix()
			dir.modNsec = mtime.Nanosecond()
		}
	}
	dir.mu.Unlock()
	return n
}

func (fsys *FS) Remove(name string) error {
	var err error
	{
		if !fs.ValidPath(name) {
			err = syscall.EINVAL
			goto error
		}
		if name == "." {
			err = syscall.ENOTSUP
			goto error
		}
		dir, base := findDir(&fsys.root, name)
		if dir == nil {
			name = base
			err = syscall.ENOENT
			goto error
		}
		if dir.fileFS != nil {
			name = base
			err = syscall.ENOTDIR
			goto error
		}
		n := unlink(dir, base)
		if n == nil {
			err = syscall.ENOENT
			goto error
		}
		atomic.AddInt32(&fsys.items, -1)
		atomic.AddInt64(&fsys.size, -size(n))
		return nil
	}
error:
	return &fs.PathError{Op: "remove", Path: name, Err: err}
}

func (fsys *FS) Rename(oldname, newname string) error {
	var (
		err error
		n   *node
	)
	olddir, oldbase := findDir(&fsys.root, oldname)
	{
		if olddir == nil || olddir.fileFS != nil {
			oldbase = oldname
			err = syscall.ENOENT
			goto error
		}
		n = unlink(olddir, oldbase)
		if n == nil {
			oldbase = oldname
			err = syscall.ENOENT
			goto error
		}
		newdir, newbase := findDir(&fsys.root, newname)
		if newdir == nil {
			oldbase = newbase
			err = syscall.ENOENT
			goto error
		}
		if newdir.fileFS != nil {
			oldbase = newbase
			err = syscall.ENOTDIR
			goto error
		}
		// BUG: may be another file with the same name
		n.name = newbase
		newdir.mu.Lock()
		n.next = newdir.list
		newdir.list = n
		mtime := time.Now()
		newdir.modSec = mtime.Unix()
		newdir.modNsec = mtime.Nanosecond()
		newdir.mu.Unlock()
		return nil
	}
error:
	if n != nil {
		olddir.mu.Lock()
		n.next = olddir.list
		olddir.list = n
		olddir.mu.Unlock()
	}
	return &fs.PathError{Op: "rename", Path: oldbase, Err: err}
}

type fileInfo struct {
	modSec  int64
	modNsec int
	name    string
	size    int
	isDir   bool
}

func (fi *fileInfo) Name() string     { return fi.name }
func (fi *fileInfo) Size() int64      { return int64(fi.size) }
func (fi *fileInfo) IsDir() bool      { return fi.isDir }
func (fi *fileInfo) Sys() interface{} { return nil }

func (fi *fileInfo) ModTime() time.Time {
	return time.Unix(fi.modSec, int64(fi.modNsec))
}

func (fi *fileInfo) Mode() fs.FileMode {
	if fi.isDir {
		return fs.ModeDir | 0777
	}
	return 0666
}

// Additional methods to implement fs.DirEntry interface
func (fi *fileInfo) Type() fs.FileMode          { return fi.Mode() }
func (fi *fileInfo) Info() (fs.FileInfo, error) { return fi, nil }
