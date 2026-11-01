// Package byml reads and writes Nintendo BYAML v2/v3 (little endian), the binary data format
// Splatoon 2's BCAT files use. Written from the public format description
// (https://zeldamods.github.io/oead/ and the Mario Kart / Zelda modding wikis), not from any
// third-party code. Only the node types those files use are supported: string, bool, int,
// uint, float, array, hash and null.
package byml

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"sort"
)

// Hash is a BYAML dictionary. Values are string, bool, int32, uint32, float32, nil, []any, Hash.
type Hash map[string]any

const (
	tString = 0xa0
	tArray  = 0xc0
	tHash   = 0xc1
	tStrTab = 0xc2
	tBool   = 0xd0
	tInt    = 0xd1
	tFloat  = 0xd2
	tUint   = 0xd3
	tNull   = 0xff
)

var le = binary.LittleEndian

type reader struct {
	b     []byte
	names []string
	strs  []string
}

func u24(b []byte) int { return int(b[0]) | int(b[1])<<8 | int(b[2])<<16 }

// Decode parses a BYAML document.
func Decode(b []byte) (any, error) {
	if len(b) < 16 || string(b[:2]) != "YB" {
		return nil, errors.New("byml: not a little-endian BYAML")
	}
	if v := le.Uint16(b[2:]); v < 2 || v > 3 {
		return nil, fmt.Errorf("byml: unsupported version %d", v)
	}
	r := &reader{b: b}
	var err error
	if off := le.Uint32(b[4:]); off != 0 {
		if r.names, err = r.table(int(off)); err != nil {
			return nil, err
		}
	}
	if off := le.Uint32(b[8:]); off != 0 {
		if r.strs, err = r.table(int(off)); err != nil {
			return nil, err
		}
	}
	root := le.Uint32(b[12:])
	if root == 0 {
		return nil, nil
	}
	return r.container(int(root))
}

func (r *reader) table(off int) ([]string, error) {
	if off+4 > len(r.b) || r.b[off] != tStrTab {
		return nil, errors.New("byml: bad string table")
	}
	n := u24(r.b[off+1:])
	if off+4+4*(n+1) > len(r.b) {
		return nil, errors.New("byml: truncated string table")
	}
	out := make([]string, n)
	for i := range out {
		s := off + int(le.Uint32(r.b[off+4+4*i:]))
		if s >= len(r.b) {
			return nil, errors.New("byml: string out of range")
		}
		e := s
		for e < len(r.b) && r.b[e] != 0 {
			e++
		}
		out[i] = string(r.b[s:e])
	}
	return out, nil
}

func (r *reader) container(off int) (any, error) {
	if off+4 > len(r.b) {
		return nil, errors.New("byml: node out of range")
	}
	t, n := r.b[off], u24(r.b[off+1:])
	switch t {
	case tArray:
		vals := off + 4 + (n+3)&^3
		if vals+4*n > len(r.b) {
			return nil, errors.New("byml: truncated array")
		}
		out := make([]any, n)
		for i := range out {
			v, err := r.value(r.b[off+4+i], le.Uint32(r.b[vals+4*i:]))
			if err != nil {
				return nil, err
			}
			out[i] = v
		}
		return out, nil
	case tHash:
		if off+4+8*n > len(r.b) {
			return nil, errors.New("byml: truncated hash")
		}
		out := make(Hash, n)
		for i := 0; i < n; i++ {
			e := off + 4 + 8*i
			k := u24(r.b[e:])
			if k >= len(r.names) {
				return nil, errors.New("byml: key index out of range")
			}
			v, err := r.value(r.b[e+3], le.Uint32(r.b[e+4:]))
			if err != nil {
				return nil, err
			}
			out[r.names[k]] = v
		}
		return out, nil
	}
	return nil, fmt.Errorf("byml: container type %#x", t)
}

func (r *reader) value(t byte, v uint32) (any, error) {
	switch t {
	case tString:
		if int(v) >= len(r.strs) {
			return nil, errors.New("byml: string index out of range")
		}
		return r.strs[v], nil
	case tBool:
		return v != 0, nil
	case tInt:
		return int32(v), nil
	case tUint:
		return v, nil
	case tFloat:
		return math.Float32frombits(v), nil
	case tNull:
		return nil, nil
	case tArray, tHash:
		return r.container(int(v))
	}
	return nil, fmt.Errorf("byml: unsupported node type %#x", t)
}

// ---- writer ----

type writer struct {
	names, strs map[string]int
	nameList    []string
	strList     []string
}

func typeOf(v any) (byte, error) {
	switch v.(type) {
	case string:
		return tString, nil
	case bool:
		return tBool, nil
	case int32, int:
		return tInt, nil
	case uint32:
		return tUint, nil
	case float32:
		return tFloat, nil
	case nil:
		return tNull, nil
	case []any:
		return tArray, nil
	case Hash:
		return tHash, nil
	}
	return 0, fmt.Errorf("byml: cannot encode %T", v)
}

// collect gathers every key and string value so both tables can be sorted before any index is used.
func (w *writer) collect(v any) {
	switch x := v.(type) {
	case string:
		w.strs[x] = 0
	case []any:
		for _, e := range x {
			w.collect(e)
		}
	case Hash:
		for k, e := range x {
			w.names[k] = 0
			w.collect(e)
		}
	}
}

func sorted(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	for i, k := range out {
		m[k] = i
	}
	return out
}

func align4(b []byte) []byte {
	for len(b)%4 != 0 {
		b = append(b, 0)
	}
	return b
}

func tableBytes(list []string) []byte {
	n := len(list)
	b := make([]byte, 4+4*(n+1))
	b[0] = tStrTab
	b[1], b[2], b[3] = byte(n), byte(n>>8), byte(n>>16)
	off := len(b)
	for i, s := range list {
		le.PutUint32(b[4+4*i:], uint32(off))
		b = append(b, s...)
		b = append(b, 0)
		off = len(b)
	}
	le.PutUint32(b[4+4*n:], uint32(off))
	return align4(b)
}

// Encode serialises root (a Hash or []any) as BYAML version 2.
func Encode(root any) ([]byte, error) {
	if t, err := typeOf(root); err != nil || (t != tHash && t != tArray) {
		return nil, errors.New("byml: root must be a hash or an array")
	}
	w := &writer{names: map[string]int{}, strs: map[string]int{}}
	w.collect(root)
	w.nameList, w.strList = sorted(w.names), sorted(w.strs)
	out := make([]byte, 16)
	copy(out, "YB")
	le.PutUint16(out[2:], 2)
	if len(w.nameList) > 0 {
		le.PutUint32(out[4:], uint32(len(out)))
		out = append(out, tableBytes(w.nameList)...)
	}
	if len(w.strList) > 0 {
		le.PutUint32(out[8:], uint32(len(out)))
		out = append(out, tableBytes(w.strList)...)
	}
	le.PutUint32(out[12:], uint32(len(out)))
	out, _, err := w.node(out, root)
	return out, err
}

// node appends a container and everything below it, returning the buffer and the container's offset.
func (w *writer) node(out []byte, v any) ([]byte, int, error) {
	start := len(out)
	switch x := v.(type) {
	case []any:
		n := len(x)
		hdr := make([]byte, 4+(n+3)&^3+4*n)
		hdr[0] = tArray
		hdr[1], hdr[2], hdr[3] = byte(n), byte(n>>8), byte(n>>16)
		vals := 4 + (n+3)&^3
		out = append(out, hdr...)
		for i, e := range x {
			t, word, o, err := w.leaf(out, e)
			if err != nil {
				return nil, 0, err
			}
			out = o
			out[start+4+i] = t
			le.PutUint32(out[start+vals+4*i:], word)
		}
	case Hash:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool { return w.names[keys[i]] < w.names[keys[j]] })
		n := len(keys)
		hdr := make([]byte, 4+8*n)
		hdr[0] = tHash
		hdr[1], hdr[2], hdr[3] = byte(n), byte(n>>8), byte(n>>16)
		out = append(out, hdr...)
		for i, k := range keys {
			t, word, o, err := w.leaf(out, x[k])
			if err != nil {
				return nil, 0, err
			}
			out = o
			e := start + 4 + 8*i
			idx := w.names[k]
			out[e], out[e+1], out[e+2], out[e+3] = byte(idx), byte(idx>>8), byte(idx>>16), t
			le.PutUint32(out[e+4:], word)
		}
	}
	return out, start, nil
}

func (w *writer) leaf(out []byte, v any) (byte, uint32, []byte, error) {
	t, err := typeOf(v)
	if err != nil {
		return 0, 0, nil, err
	}
	switch x := v.(type) {
	case string:
		return t, uint32(w.strs[x]), out, nil
	case bool:
		if x {
			return t, 1, out, nil
		}
		return t, 0, out, nil
	case int:
		return t, uint32(int32(x)), out, nil
	case int32:
		return t, uint32(x), out, nil
	case uint32:
		return t, x, out, nil
	case float32:
		return t, math.Float32bits(x), out, nil
	case nil:
		return t, 0, out, nil
	}
	out, off, err := w.node(out, v)
	return t, uint32(off), out, err
}
