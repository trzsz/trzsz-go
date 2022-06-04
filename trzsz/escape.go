/*
MIT License

Copyright (c) 2022 Lonny Wong

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
*/

package trzsz

import (
	"bytes"
	"fmt"
	"strconv"

	"golang.org/x/text/encoding/charmap"
)

type unicode string

func (s unicode) MarshalJSON() ([]byte, error) {
	b := new(bytes.Buffer)
	b.WriteByte('"')
	for _, c := range s {
		if c < 128 && strconv.IsPrint(c) {
			b.WriteRune(c)
		} else {
			b.WriteString(fmt.Sprintf("\\u%04x", c))
		}
	}
	b.WriteByte('"')
	return b.Bytes(), nil
}

func getEscapeChars(escapeAll bool) [][]unicode {
	escapeChars := [][]unicode{
		{"\u00ee", "\u00ee\u00ee"},
		{"\u007e", "\u00ee\u0031"},
	}
	if escapeAll {
		const chars = unicode("\x02\x10\x1b\x1d\u009d")
		for i, c := range chars {
			escapeChars = append(escapeChars, []unicode{unicode(c), "\u00ee" + unicode(byte(i+0x41))})
		}
	}
	return escapeChars
}

func escapeCharsToCodes(escapeChars []interface{}) ([][]byte, error) {
	escapeCodes := make([][]byte, len(escapeChars))
	encoder := charmap.ISO8859_1.NewEncoder()
	for i, v := range escapeChars {
		a, ok := v.([]interface{})
		if !ok {
			return nil, newTrzszError(fmt.Sprintf("escape chars invalid: %#v", v))
		}
		if len(a) != 2 {
			return nil, newTrzszError(fmt.Sprintf("escape chars invalid: %#v", v))
		}
		b, ok := a[0].(string)
		if !ok {
			return nil, newTrzszError(fmt.Sprintf("escape chars invalid: %#v", v))
		}
		bb, err := encoder.Bytes([]byte(b))
		if err != nil {
			return nil, err
		}
		if len(bb) != 1 {
			return nil, newTrzszError(fmt.Sprintf("escape chars invalid: %#v", v))
		}
		c, ok := a[1].(string)
		if !ok {
			return nil, newTrzszError(fmt.Sprintf("escape chars invalid: %#v", v))
		}
		cc, err := encoder.Bytes([]byte(c))
		if err != nil {
			return nil, err
		}
		if len(cc) != 2 {
			return nil, newTrzszError(fmt.Sprintf("escape chars invalid: %#v", v))
		}
		escapeCodes[i] = make([]byte, 3)
		escapeCodes[i][0] = bb[0]
		escapeCodes[i][1] = cc[0]
		escapeCodes[i][2] = cc[1]
	}
	return escapeCodes, nil
}

func escapeData(data []byte, escapeCodes [][]byte) []byte {
	if len(escapeCodes) == 0 {
		return data
	}

	buf := make([]byte, len(data)*2)
	idx := 0
	for _, d := range data {
		escapeIdx := -1
		for j, e := range escapeCodes {
			if d == e[0] {
				escapeIdx = j
				break
			}
		}
		if escapeIdx < 0 {
			buf[idx] = d
			idx++
		} else {
			buf[idx] = escapeCodes[escapeIdx][1]
			idx++
			buf[idx] = escapeCodes[escapeIdx][2]
			idx++
		}
	}
	return buf[:idx]
}

func unescapeData(data []byte, escapeCodes [][]byte) []byte {
	if len(escapeCodes) == 0 {
		return data
	}

	size := len(data)
	buf := make([]byte, size)
	idx := 0
	for i := 0; i < size; i++ {
		escapeIdx := -1
		if i < size-1 {
			for j, e := range escapeCodes {
				if data[i] == e[1] && data[i+1] == e[2] {
					escapeIdx = j
					break
				}
			}
		}
		if escapeIdx < 0 {
			buf[idx] = data[i]
			idx++
		} else {
			buf[idx] = escapeCodes[escapeIdx][0]
			idx++
			i++
		}
	}
	return buf[:idx]
}
