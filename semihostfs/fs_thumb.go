// Copyright 2024 The Embedded Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package semihostfs

import (
	"io/fs"
	"path/filepath"
	"syscall"
	"unsafe"
)

// https://github.com/ARM-software/abi-aa/blob/main/semihosting/semihosting.rst

func openWithFinalizer(fsys *FS, name string, flag int, _ fs.FileMode, closed func()) (f fs.File, err error) {
	mode := 0
	switch {
	case flag&syscall.O_RDONLY != 0:
		// rb: open binary file for reading from the beggining
		mode = 1
	case flag&syscall.O_RDWR != 0:
		switch flag & (syscall.O_CREAT | syscall.O_TRUNC | syscall.O_APPEND) {
		case 0:
			// r+b: open binary file for read/writing at the beggining
			mode = 3
		case syscall.O_CREAT | syscall.O_TRUNC:
			// w+b: truncate or create binary file for writing and reading
			mode = 7
		case syscall.O_CREAT | syscall.O_APPEND:
			// a+b: open or create binary file for appending and reading
			mode = 11
		}
	case flag&syscall.O_WRONLY != 0:
		switch flag & (syscall.O_CREAT | syscall.O_TRUNC | syscall.O_APPEND) {
		case syscall.O_CREAT | syscall.O_TRUNC:
			// wb: truncate or create binary file for writing
			mode = 5
		case syscall.O_CREAT | syscall.O_APPEND:
			// ab: open or create text file for appending
			mode = 9
		}
	}
	hostPath := ":tt"
	switch name {
	case ":stderr":
		mode = 8
	case ":stdout":
		mode = 4
	case ":stdin":
		mode = 0
	default:
		hostPath = filepath.Join(fsys.root, name)
	}
	type args struct {
		path    *byte
		mode    int
		pathLen int
	}
	aptr := &args{
		unsafe.StringData(hostPath + "\x00"),
		mode,
		len(hostPath),
	}
	fd := hostCall(0x01, unsafe.Pointer(aptr))
	if fd == -1 {
		err = hostError()
		return
	}
	f = &file{name, fd, closed}
	return
}

func mkdir(fsys *FS, name string, mode fs.FileMode) error {
	return syscall.ENOTSUP
}

func remove(fsys *FS, name string) error {
	type args struct {
		path    *byte
		pathLen int
	}
	hostPath := filepath.Join(fsys.root, name)
	aptr := &args{
		unsafe.StringData(hostPath + "\x00"),
		len(hostPath),
	}
	if errno := hostCall(0x0e, unsafe.Pointer(aptr)); errno != 0 {
		return &Error{errno}
	}
	return nil
}

func rename(fsys *FS, oldname, newname string) error {
	type args struct {
		oldName *byte
		oldLen  int
		newName *byte
		newLen  int
	}
	hostOld := filepath.Join(fsys.root, oldname)
	hostNew := filepath.Join(fsys.root, newname)
	aptr := &args{
		unsafe.StringData(hostOld + "\x00"),
		len(hostOld),
		unsafe.StringData(hostNew + "\x00"),
		len(hostNew),
	}
	if errno := hostCall(0x0f, unsafe.Pointer(aptr)); errno != 0 {
		return &Error{errno}
	}
	return nil
}
