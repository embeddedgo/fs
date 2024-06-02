// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build riscv64 || thumb

package semihostfs

import (
	"fmt"
	"unsafe"
)

// BUG: hostCall and the subsequent hostError must be protected with a mutex

//go:noescape
func hostCall(cmd int, arg unsafe.Pointer) int

type Error struct {
	no int
}

func (err *Error) Error() string {
	return fmt.Sprint("semihosting error: ", err.no)
}

func hostError() *Error {
	return &Error{hostCall(0x13, nil)}
}
