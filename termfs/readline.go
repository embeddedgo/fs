// Copyright 2020 The Embedded Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package termfs

import (
	"errors"
	"strconv"
)

const esc = '\x1b'

var ErrLineTooLong = errors.New("line too long")

func (f *file) readLine(p []byte) (n int, err error) {
	defer func() {
		if err != nil {
			f.fs.line = f.fs.line[:0]
			err = wrapErr("read", err)
		}
	}()
	x := 0
	for f.fs.rpos < 0 {
		if len(f.fs.line) == cap(f.fs.line) {
			return 0, ErrLineTooLong
		}
		buf := p[:1] // len(p) is at least 1, use it as one byte scratch buffer
		if _, err := f.fs.r.Read(buf); err != nil {
			return 0, err
		}
		c := buf[0]
		switch c {
		case '\r':
			if f.fs.charMap&InCRLF == 0 {
				continue // skip CR
			}
			c = '\n'
			buf[0] = c
			fallthrough
		case '\n':
			x = len(f.fs.line)
			f.fs.rpos = 0
		case '\x7f': //  Delete
			c = '\b'
			fallthrough
		case '\b': // Backspace
			if x == 0 {
				continue
			}
			x--
		case esc:
			if _, err := f.fs.r.Read(buf); err != nil {
				return 0, err
			}
			if buf[0] != '[' {
				continue // skip unsupported control sequence
			}
			if _, err := f.fs.r.Read(buf); err != nil {
				return 0, err
			}
			switch buf[0] {
			case 'C': // Cursor Forward
				if x == len(f.fs.line) {
					continue // end of line
				}
				f.fs.ansi[2] = 'C'
				x++
			case 'D': // Cursor Back
				if x == 0 {
					continue // beginning of the line
				}
				f.fs.ansi[2] = 'D'
				x--
			default:
				continue // skip unsupported CSI sequence
			}
			if f.fs.echo {
				if _, err := f.write(f.fs.ansi[:3]); err != nil {
					return 0, err
				}
			}
			continue
		default:
			if c < ' ' || c >= 0xFE {
				continue // skip other special characters
			}
		}
		if c != '\b' {
			// make a room for a new char
			m := len(f.fs.line)
			f.fs.line = f.fs.line[:m+1]
			if x != m {
				copy(f.fs.line[x+1:], f.fs.line[x:])
			}
		}
		f.fs.line[x] = c
		m := len(f.fs.line)
		if f.fs.echo {
			if c == '\b' {
				buf = f.fs.line[x : m+1]
				buf[m-x] = ' '
			} else {
				buf = f.fs.line[x:m]
			}
			if _, err := f.write(buf); err != nil {
				return 0, err
			}
			if n := len(buf) - 1; n > 0 {
				// move cursor n characters back
				if n == 1 {
					// use '\b' instead of CSI D to support non-ANSI terminals
					p[0] = '\b'
					buf = p[:1]
				} else if n <= 999 {
					// use CSI n D sequence
					buf = strconv.AppendInt(f.fs.ansi[:2], int64(n), 10)
					m := len(buf)
					buf = buf[:m+1]
					buf[m] = 'D'
				} else {
					return 0, ErrLineTooLong
				}
				if _, err := f.write(buf); err != nil {
					return 0, err
				}
			}
		}
		if c == '\b' {
			m--
			if x != m {
				copy(f.fs.line[x:], f.fs.line[x+1:])
			}
			f.fs.line = f.fs.line[:m]
			continue
		}
		x++
	}
	n = copy(p, f.fs.line[f.fs.rpos:])
	f.fs.rpos += n
	if f.fs.rpos == len(f.fs.line) {
		f.fs.rpos = -1
		f.fs.line = f.fs.line[:0]
	}
	return n, nil
}
