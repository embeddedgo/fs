// Copyright 2020 The Embedded Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ramfs

import (
	"io/fs"
	"syscall"
)

// A dir represents an open directory
type dir struct {
	name string
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
