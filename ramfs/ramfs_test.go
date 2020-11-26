// Copyright 2020 The Embedded Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ramfs

import (
	"bytes"
	"errors"
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

func TestRAMFS(t *testing.T) {
	const maxSize = 1024

	ramfs := New(maxSize)
	open := func(name string, flags int, perm fs.FileMode) (io.ReadWriteCloser, error) {
		f, err := ramfs.OpenWithFinalizer(name, flags, perm, nop)
		if f == nil {
			return nil, err
		}
		return f.(io.ReadWriteCloser), err
	}

	f, err := open("a.txt", 0, 0)
	expectErr(t, syscall.ENOENT, err)

	f, err = open("a.txt", syscall.O_CREAT, 0666)
	checkErr(t, err)
	data := []byte("test1234\n")
	_, err = f.Write([]byte("test\n"))
	expectErr(t, syscall.ENOTSUP, err)
	checkErr(t, f.Close())

	usedItems, maxItems, usedBytes, maxBytes := ramfs.Usage()
	if usedItems != 1 || maxItems != -1 || usedBytes != emptyNodeSize || maxBytes != maxSize {
		t.Fatal("ramfs.Usage")
	}

	f, err = open("a.txt", syscall.O_CREAT|syscall.O_EXCL, 0666)
	expectErr(t, syscall.EEXIST, err)

	f, err = open("a.txt", syscall.O_WRONLY, 0666)
	checkErr(t, err)
	checkWrite(t, f, data)
	checkWrite(t, f, data)
	checkErr(t, f.Close())

	buf := make([]byte, 100)
	f, err = open("a.txt", 0, 0)
	checkErr(t, err)
	checkRead(t, f, buf, data)
	checkRead(t, f, buf, data)
	_, err = f.Read(buf)
	expectErr(t, io.EOF, err)
	checkErr(t, f.Close())

	f, err = open("a.txt", syscall.O_WRONLY, 0666)
	checkErr(t, err)
	checkWrite(t, f, data)
	checkErr(t, f.Close())

	f, err = open("a.txt", 0, 0)
	checkErr(t, err)
	checkRead(t, f, buf, data)
	_, err = f.Read(buf)
	expectErr(t, io.EOF, err)
	checkErr(t, f.Close())
}
