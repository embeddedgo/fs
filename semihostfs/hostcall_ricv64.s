// Copyright 2024 The Embedded Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

#include "textflag.h"

// https://github.com/riscv-non-isa/riscv-semihosting/blob/main/riscv-semihosting.adoc

// func hostCall(cmd int, arg unsafe.Pointer) int
TEXT ·hostCall(SB),NOSPLIT|NOFRAME,$0-24
	MOVW  cmd+0(FP), A0
	MOVW  arg+8(FP), A1
	SLLI  $0x1f, ZERO, ZERO
	EBREAK
	SRAI  $0x7, ZERO, ZERO
	MOVW  A0, ret+16(FP)
	RET
