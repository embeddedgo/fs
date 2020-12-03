// Copyright 2020 The Embedded Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package termfs

import "errors"

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
			buf[0] = c
			fallthrough
		case '\b': // Backspace
			if x == 0 {
				continue
			}
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
			case 'C': // ANSI Cursor Forward
				if x == len(f.fs.line) {
					continue // end of line
				}
				f.fs.ansi[3] = 'C'
				x++
			case 'D': // ANSI Cursor Back
				if x == 0 {
					continue // beginning of the line
				}
				f.fs.ansi[3] = 'D'
				x--
			default:
				continue // skip unsupported CSI sequence
			}
			if f.fs.echo {
				if _, err := f.write(f.fs.ansi[1:4]); err != nil {
					return 0, err
				}
			}
			continue
		default:
			if c < ' ' || c >= 0xFE {
				continue // skip other special characters
			}
		}
		m := len(f.fs.line)
		if f.fs.echo {
			if c == '\b' {
				if x == m {
					f.fs.ansi[3] = '\b' // this sequence deletes the last
					f.fs.ansi[4] = ' '  // character on ANSI and non-ANSI
					f.fs.ansi[5] = '\b' // terminals
					buf = f.fs.ansi[3:6]
				} else {
					f.fs.ansi[3] = 'P' // ANSI Delete Character
					buf = f.fs.ansi[:4]
				}
			} else if x != m {
				f.fs.ansi[3] = '@' // ANSI Insert Character
				f.fs.ansi[4] = c
				buf = f.fs.ansi[1:5]
			}
			if _, err := f.write(buf); err != nil {
				return 0, err
			}
		}
		if c == '\b' {
			x--
			m--
			if x != m {
				copy(f.fs.line[x:], f.fs.line[x+1:])
			}
			f.fs.line = f.fs.line[:m]
			continue
		}
		// insert new char
		f.fs.line = f.fs.line[:m+1]
		if x != m {
			copy(f.fs.line[x+1:], f.fs.line[x:])
		}
		f.fs.line[x] = c
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
