// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package semihostfs

import (
	"io"
	"io/fs"
	"path/filepath"
	"time"
	"unsafe"
)

type file struct {
	name   string
	fd     int
	closed func()
}

func (f *file) Close() (err error) {
	if hostCall(0x02, unsafe.Pointer(&f.fd)) == -1 {
		err = hostError()
	}
	f.closed()
	f.closed = nil
	return
}

type rwargs struct {
	fd int
	p  *byte
	n  int
}

func (f *file) Read(p []byte) (n int, err error) {
	if len(p) == 0 {
		return
	}
	aptr := &rwargs{
		f.fd,
		unsafe.SliceData(p),
		len(p),
	}
	notRead := hostCall(0x06, unsafe.Pointer(aptr))
	n = len(p) - notRead
	if n == 0 {
		err = io.EOF
	}
	return
}

func (f *file) WriteString(s string) (n int, err error) {
	if len(s) == 0 {
		return
	}
	aptr := &rwargs{
		f.fd,
		unsafe.StringData(s),
		len(s),
	}
	notWritten := hostCall(0x05, unsafe.Pointer(aptr))
	n = len(s) - notWritten
	if notWritten != 0 {
		err = hostError()
	}
	return

}

func (f *file) Write(p []byte) (int, error) {
	return f.WriteString(*(*string)(unsafe.Pointer(&p)))
}

type fileInfo struct {
	name string
	size int
}

func (f *file) Stat() (fi fs.FileInfo, err error) {
	size := hostCall(0x0c, unsafe.Pointer(&f.fd))
	if size == -1 {
		err = hostError()
		return
	}
	fi = &fileInfo{
		filepath.Base(f.name),
		size,
	}
	return
}

func (fi *fileInfo) Name() string       { return fi.name }
func (fi *fileInfo) Size() int64        { return int64(fi.size) }
func (fi *fileInfo) Mode() fs.FileMode  { return 0666 }
func (fi *fileInfo) ModTime() time.Time { return time.Time{} }
func (fi *fileInfo) IsDir() bool        { return false }
func (fi *fileInfo) Sys() any           { return nil }
