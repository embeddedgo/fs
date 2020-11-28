// Copyright 2020 The Embedded Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ramfs

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"syscall"
	"testing"
)

func checkErr(t *testing.T, err error) {
	if err != nil {
		t.Fatal(err)
	}
}

func expectErr(t *testing.T, expect, got error) {
	if !errors.Is(got, expect) {
		t.Fatalf("expected '%v' error, got '%v'", expect, got)
	}
}

func checkWrite(t *testing.T, w io.Writer, data []byte) {
	n, err := w.Write(data)
	checkErr(t, err)
	if n != len(data) {
		t.Fatalf("write: expected %d bytes, got %d", len(data), n)
	}
}

func checkRead(t *testing.T, r io.Reader, buf, expect []byte) {
	n, err := r.Read(buf[:len(expect)])
	checkErr(t, err)
	if !bytes.Equal(buf[:n], expect) {
		t.Fatalf("read: expected %s bytes, got %s", expect, buf[:n])
	}
}

func checkUsage(t *testing.T, fsys *FS, usedItems int, usedBytes, maxBytes int) {
	ui, mi, ub, mb := fsys.Usage()
	if ui != usedItems || mi != -1 || ub != int64(usedBytes) || mb != int64(maxBytes) {
		t.Fatalf(
			"expected usage: %d, -1, %d B, %d B, got: %d, %d, %d B, %d B",
			usedItems, usedBytes, maxBytes, ui, mi, ub, mb,
		)
	}
}

type rwFile interface {
	fs.File
	io.Writer
}

func TestFS(t *testing.T) {
	const maxSize = 1024

	ramfs := New(maxSize)
	open := func(name string, flags int, perm fs.FileMode) (rwFile, error) {
		f, err := ramfs.OpenWithFinalizer(name, flags, perm, nop)
		if f == nil {
			return nil, err
		}
		return f.(rwFile), err
	}

	f, err := open("a.txt", 0, 0)
	expectErr(t, syscall.ENOENT, err)

	f, err = open("a.txt", syscall.O_CREAT, 0)
	checkErr(t, err)
	data := []byte("test1234\n")
	_, err = f.Write([]byte("test\n"))
	expectErr(t, syscall.ENOTSUP, err)
	checkErr(t, f.Close())

	checkUsage(t, ramfs, 1, emptyFileSize, maxSize)

	f, err = open("a.txt", syscall.O_CREAT|syscall.O_EXCL, 0)
	expectErr(t, syscall.EEXIST, err)

	f, err = open("a.txt", syscall.O_WRONLY, 0)
	checkErr(t, err)
	checkWrite(t, f, data)
	checkWrite(t, f, data)
	checkErr(t, f.Close())

	checkUsage(t, ramfs, 1, emptyFileSize+2*len(data), maxSize)

	buf := make([]byte, 100)
	f, err = open("a.txt", 0, 0)
	checkErr(t, err)
	checkRead(t, f, buf, data)
	checkRead(t, f, buf, data)
	_, err = f.Read(buf)
	expectErr(t, io.EOF, err)
	checkErr(t, f.Close())

	f, err = open("a.txt", syscall.O_WRONLY, 0)
	checkErr(t, err)
	checkWrite(t, f, data)
	checkErr(t, f.Close())

	checkUsage(t, ramfs, 1, emptyFileSize+2*len(data), maxSize)

	f, err = open("a.txt", 0, 0)
	checkErr(t, err)
	checkRead(t, f, buf, data)
	_, err = f.Read(buf)
	expectErr(t, io.EOF, err)
	checkErr(t, f.Close())

	checkErr(t, ramfs.Mkdir("D", 0))

	checkUsage(t, ramfs, 2, emptyFileSize+2*len(data)+dirSize, maxSize)

	checkErr(t, ramfs.Rename("a.txt", "D/b.txt"))

	checkUsage(t, ramfs, 2, emptyFileSize+2*len(data)+dirSize, maxSize)

	f, err = open("D/b.txt", syscall.O_RDONLY, 0)
	checkErr(t, err)
	fi, err := f.Stat()
	checkErr(t, err)
	checkErr(t, f.Close())
	fmt.Println("name:", fi.Name())
	fmt.Println("size:", fi.Size())
	fmt.Println("modTime:", fi.ModTime())
	fmt.Println("isDir:", fi.IsDir())
	fmt.Println("mode:", fi.Mode())

	expectErr(t, syscall.ENOENT, ramfs.Remove("a.txt"))
	checkErr(t, ramfs.Remove("D/b.txt"))

	checkUsage(t, ramfs, 1, dirSize, maxSize)
}
