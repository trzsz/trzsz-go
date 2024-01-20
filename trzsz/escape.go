/*
MIT License

Copyright (c) 2022-2024 The Trzsz Authors.

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
	"encoding/json"
	"fmt"
	"strconv"

	"golang.org/x/text/encoding/charmap"
)

type unicode string

type escapeTable struct {
	totalCount    int
	escapeCodes   []*byte
	unescapeCodes []*byte
}

const escapeLeaderByte = byte('\xee')

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
		const chars = unicode("\x02\x0d\x10\x11\x13\x18\x1b\x1d\u008d\u0090\u0091\u0093\u009d")
		e := byte('A')
		for _, c := range chars {
			escapeChars = append(escapeChars, []unicode{unicode(c), unicode(escapeLeaderByte) + unicode(e)})
			e += 1
		}
	}
	return escapeChars
}

func escapeCharsToTable(escapeChars []interface{}) (*escapeTable, error) {
	table := &escapeTable{
		totalCount:    len(escapeChars),
		escapeCodes:   make([]*byte, 256),
		unescapeCodes: make([]*byte, 256),
	}
	encoder := charmap.ISO8859_1.NewEncoder()
	for _, v := range escapeChars {
		a, ok := v.([]interface{})
		if !ok {
			return nil, simpleTrzszError("Escape chars invalid: %v", v)
		}
		if len(a) != 2 {
			return nil, simpleTrzszError("Escape chars invalid: %v", v)
		}
		b, ok := a[0].(string)
		if !ok {
			return nil, simpleTrzszError("Escape chars invalid: %v", v)
		}
		bb, err := encoder.Bytes([]byte(b))
		if err != nil {
			return nil, err
		}
		if len(bb) != 1 {
			return nil, simpleTrzszError("Escape chars invalid: %v", v)
		}
		c, ok := a[1].(string)
		if !ok {
			return nil, simpleTrzszError("Escape chars invalid: %v", v)
		}
		cc, err := encoder.Bytes([]byte(c))
		if err != nil {
			return nil, err
		}
		if len(cc) != 2 {
			return nil, simpleTrzszError("Escape chars invalid: %v", v)
		}
		if cc[0] != escapeLeaderByte {
			return nil, simpleTrzszError("Escape chars invalid: %v", v)
		}
		table.escapeCodes[bb[0]] = &cc[1]
		table.unescapeCodes[cc[1]] = &bb[0]
	}
	return table, nil
}

func (c *escapeTable) UnmarshalJSON(data []byte) error {
	var codes []interface{}
	if err := json.Unmarshal(data, &codes); err != nil {
		return err
	}
	table, err := escapeCharsToTable(codes)
	if err != nil {
		return err
	}
	*c = *table
	return nil
}

func escapeData(data []byte, table *escapeTable) []byte {
	if table == nil || table.totalCount == 0 {
		return data
	}

	buf := make([]byte, len(data)*2)
	idx := 0
	for _, bdata := range data {
		ecode := table.escapeCodes[bdata]
		if ecode == nil {
			buf[idx] = bdata
			idx++
		} else {
			buf[idx] = escapeLeaderByte
			idx++
			buf[idx] = *ecode
			idx++
		}
	}
	return buf[:idx]
}

func unescapeData(data []byte, table *escapeTable, dst []byte) ([]byte, []byte, error) {
	if table == nil || table.totalCount == 0 {
		return data, nil, nil
	}

	size := len(data)
	buf := dst
	if len(buf) == 0 {
		buf = make([]byte, size)
	}
	idx := 0
	for i := 0; i < size; i++ {
		if data[i] == escapeLeaderByte {
			if i == size-1 {
				return buf[:idx], data[i:], nil
			}
			i++
			ecode := table.unescapeCodes[data[i]]
			if ecode == nil {
				return nil, nil, simpleTrzszError("Unknown escape code: %v", data[i])
			}
			buf[idx] = *ecode
		} else {
			buf[idx] = data[i]
		}
		idx++
		if idx == len(buf) {
			return buf[:idx], data[i+1:], nil
		}
	}
	return buf[:idx], nil, nil
}
