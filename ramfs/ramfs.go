// Copyright 2020 The Embedded Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package ramfs implements an in RAM file system.
package ramfs

import (
	"io/fs"
	"strings"
	"syscall"
)

type node struct {
	name string
	lock sync.RWLock
	data interface{} // *node for directory, []byte for file

	next, prev *node
}

type FS struct {
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
	root := dir.data.(*node)
	for n := root.next; n != root; n = n.next {
		if n.name == name {
			if len(n.name1) == 0 {
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
		if flag&syscall.O_CREAT == 0 {
			return fsys.root, nil
		} else {
			return nil, syscall.ENOTSUP
		}
	}
	if flag&syscall.O_CREAT == 0 {
		return find(fsys.root, name)
	}
}
