package airplay

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/xml"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
)

// Property lists decode to nil, bool, int64, float64, string, []byte,
// []interface{} and map[string]interface{}.

var errBadPlist = errors.New("airplay: malformed property list")

// decodePlist decodes a binary or XML property list whose root is a dict.
func decodePlist(b []byte) (map[string]interface{}, error) {
	var v interface{}
	var err error
	if bytes.HasPrefix(b, []byte("bplist00")) {
		v, err = decodeBinaryPlist(b)
	} else {
		v, err = decodeXMLPlist(b)
	}
	if err != nil {
		return nil, err
	}
	m, ok := v.(map[string]interface{})
	if !ok {
		return nil, errBadPlist
	}
	return m, nil
}

// Binary property list (bplist00).

type bplistDecoder struct {
	b        []byte
	offsets  []uint64
	refSize  int
	visiting map[uint64]bool
}

func decodeBinaryPlist(b []byte) (interface{}, error) {
	if len(b) < 8+32 {
		return nil, errBadPlist
	}
	t := b[len(b)-32:]
	offSize, refSize := int(t[6]), int(t[7])
	numObjects := binary.BigEndian.Uint64(t[8:])
	top := binary.BigEndian.Uint64(t[16:])
	tableOff := binary.BigEndian.Uint64(t[24:])
	if offSize < 1 || offSize > 8 || refSize < 1 || refSize > 8 ||
		numObjects > uint64(len(b)) || tableOff > uint64(len(b)) ||
		tableOff+numObjects*uint64(offSize) > uint64(len(b)) {
		return nil, errBadPlist
	}
	d := &bplistDecoder{b: b, refSize: refSize, visiting: map[uint64]bool{}}
	for i := uint64(0); i < numObjects; i++ {
		p := tableOff + i*uint64(offSize)
		d.offsets = append(d.offsets, readUint(b[p:p+uint64(offSize)]))
	}
	return d.object(top)
}

func readUint(b []byte) uint64 {
	var v uint64
	for _, c := range b {
		v = v<<8 | uint64(c)
	}
	return v
}

func (d *bplistDecoder) bytes(off uint64, n uint64) ([]byte, error) {
	if off > uint64(len(d.b)) || n > uint64(len(d.b))-off {
		return nil, errBadPlist
	}
	return d.b[off : off+n], nil
}

// count reads the length of a variable size object, returning it and the
// offset of the object's payload.
func (d *bplistDecoder) count(off uint64) (uint64, uint64, error) {
	n := uint64(d.b[off] & 0x0F)
	off++
	if n != 0x0F {
		return n, off, nil
	}
	if off >= uint64(len(d.b)) || d.b[off]&0xF0 != 0x10 {
		return 0, 0, errBadPlist
	}
	size := uint64(1) << (d.b[off] & 0x0F)
	v, err := d.bytes(off+1, size)
	if err != nil {
		return 0, 0, err
	}
	return readUint(v), off + 1 + size, nil
}

func (d *bplistDecoder) object(ref uint64) (interface{}, error) {
	if ref >= uint64(len(d.offsets)) || d.visiting[ref] {
		return nil, errBadPlist
	}
	d.visiting[ref] = true
	defer delete(d.visiting, ref)

	off := d.offsets[ref]
	if off >= uint64(len(d.b)) {
		return nil, errBadPlist
	}
	marker := d.b[off]
	switch marker & 0xF0 {
	case 0x00:
		switch marker {
		case 0x08:
			return false, nil
		case 0x09:
			return true, nil
		}
		return nil, nil
	case 0x10:
		v, err := d.bytes(off+1, 1<<(marker&0x0F))
		if err != nil {
			return nil, err
		}
		if len(v) > 8 {
			v = v[len(v)-8:]
		}
		return int64(readUint(v)), nil
	case 0x20, 0x30: // real, date
		v, err := d.bytes(off+1, 1<<(marker&0x0F))
		if err != nil {
			return nil, err
		}
		switch len(v) {
		case 4:
			return float64(math.Float32frombits(binary.BigEndian.Uint32(v))), nil
		case 8:
			return math.Float64frombits(binary.BigEndian.Uint64(v)), nil
		}
		return nil, errBadPlist
	case 0x40, 0x50, 0x60:
		n, p, err := d.count(off)
		if err != nil {
			return nil, err
		}
		if marker&0xF0 == 0x60 {
			v, err := d.bytes(p, n*2)
			if err != nil {
				return nil, err
			}
			u := make([]uint16, n)
			for i := range u {
				u[i] = binary.BigEndian.Uint16(v[i*2:])
			}
			return string(utf16.Decode(u)), nil
		}
		v, err := d.bytes(p, n)
		if err != nil {
			return nil, err
		}
		if marker&0xF0 == 0x50 {
			return string(v), nil
		}
		return append([]byte(nil), v...), nil
	case 0x80: // UID
		v, err := d.bytes(off+1, uint64(marker&0x0F)+1)
		if err != nil {
			return nil, err
		}
		return int64(readUint(v)), nil
	case 0xA0, 0xD0:
		n, p, err := d.count(off)
		if err != nil {
			return nil, err
		}
		refs := n
		if marker&0xF0 == 0xD0 {
			refs *= 2
		}
		if refs > uint64(len(d.b)) {
			return nil, errBadPlist
		}
		raw, err := d.bytes(p, refs*uint64(d.refSize))
		if err != nil {
			return nil, err
		}
		ref := func(i uint64) uint64 {
			return readUint(raw[i*uint64(d.refSize) : (i+1)*uint64(d.refSize)])
		}
		if marker&0xF0 == 0xA0 {
			arr := make([]interface{}, 0, n)
			for i := uint64(0); i < n; i++ {
				v, err := d.object(ref(i))
				if err != nil {
					return nil, err
				}
				arr = append(arr, v)
			}
			return arr, nil
		}
		dict := make(map[string]interface{}, n)
		for i := uint64(0); i < n; i++ {
			k, err := d.object(ref(i))
			if err != nil {
				return nil, err
			}
			ks, ok := k.(string)
			if !ok {
				return nil, errBadPlist
			}
			v, err := d.object(ref(n + i))
			if err != nil {
				return nil, err
			}
			dict[ks] = v
		}
		return dict, nil
	}
	return nil, fmt.Errorf("airplay: unsupported plist object 0x%02x", marker)
}

// encodeBinaryPlist encodes v, which may contain bool, int, int64,
// float64, string, []byte, []interface{} and map[string]interface{}.
func encodeBinaryPlist(v interface{}) ([]byte, error) {
	e := &bplistEncoder{}
	// Object references are 2 bytes, enough for the small documents sent
	// to devices.
	e.refSize = 2
	if _, err := e.add(v); err != nil {
		return nil, err
	}
	if len(e.objects) > 0xFFFF {
		return nil, errors.New("airplay: plist too large")
	}

	out := []byte("bplist00")
	offsets := make([]uint64, len(e.objects))
	for i, o := range e.objects {
		offsets[i] = uint64(len(out))
		out = append(out, o...)
	}
	tableOff := uint64(len(out))
	offSize := 1
	for tableOff >= 1<<(8*uint(offSize)) {
		offSize++
	}
	for _, o := range offsets {
		out = appendUint(out, o, offSize)
	}
	trailer := make([]byte, 32)
	trailer[6] = byte(offSize)
	trailer[7] = byte(e.refSize)
	binary.BigEndian.PutUint64(trailer[8:], uint64(len(e.objects)))
	binary.BigEndian.PutUint64(trailer[24:], tableOff)
	return append(out, trailer...), nil
}

type bplistEncoder struct {
	objects [][]byte
	refSize int
}

func appendUint(b []byte, v uint64, size int) []byte {
	for i := size - 1; i >= 0; i-- {
		b = append(b, byte(v>>(8*uint(i))))
	}
	return b
}

func appendInt(b []byte, v int64) []byte {
	switch {
	case v >= 0 && v <= 0xFF:
		return append(b, 0x10, byte(v))
	case v >= 0 && v <= 0xFFFF:
		return appendUint(append(b, 0x11), uint64(v), 2)
	case v >= 0 && v <= 0xFFFFFFFF:
		return appendUint(append(b, 0x12), uint64(v), 4)
	}
	return appendUint(append(b, 0x13), uint64(v), 8)
}

func appendMarker(b []byte, typ byte, n int) []byte {
	if n < 0x0F {
		return append(b, typ|byte(n))
	}
	return appendInt(append(b, typ|0x0F), int64(n))
}

// add appends the object for v (children first is not required) and
// returns its reference.
func (e *bplistEncoder) add(v interface{}) (int, error) {
	ref := len(e.objects)
	e.objects = append(e.objects, nil)

	var o []byte
	switch v := v.(type) {
	case nil:
		o = []byte{0x00}
	case bool:
		o = []byte{0x08}
		if v {
			o[0] = 0x09
		}
	case int:
		o = appendInt(nil, int64(v))
	case int64:
		o = appendInt(nil, v)
	case float64:
		o = appendUint([]byte{0x23}, math.Float64bits(v), 8)
	case []byte:
		o = append(appendMarker(nil, 0x40, len(v)), v...)
	case string:
		if isASCII(v) {
			o = append(appendMarker(nil, 0x50, len(v)), v...)
		} else {
			u := utf16.Encode([]rune(v))
			o = appendMarker(nil, 0x60, len(u))
			for _, c := range u {
				o = append(o, byte(c>>8), byte(c))
			}
		}
	case []interface{}:
		refs := make([]int, len(v))
		for i, item := range v {
			r, err := e.add(item)
			if err != nil {
				return 0, err
			}
			refs[i] = r
		}
		o = appendMarker(nil, 0xA0, len(v))
		for _, r := range refs {
			o = appendUint(o, uint64(r), e.refSize)
		}
	case map[string]interface{}:
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		refs := make([]int, 0, len(v)*2)
		for _, k := range keys {
			r, err := e.add(k)
			if err != nil {
				return 0, err
			}
			refs = append(refs, r)
		}
		for _, k := range keys {
			r, err := e.add(v[k])
			if err != nil {
				return 0, err
			}
			refs = append(refs, r)
		}
		o = appendMarker(nil, 0xD0, len(v))
		for _, r := range refs {
			o = appendUint(o, uint64(r), e.refSize)
		}
	default:
		return 0, fmt.Errorf("airplay: cannot encode %T in plist", v)
	}
	e.objects[ref] = o
	return ref, nil
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// XML property list.

func decodeXMLPlist(b []byte) (interface{}, error) {
	dec := xml.NewDecoder(bytes.NewReader(b))
	for {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		if start, ok := tok.(xml.StartElement); ok {
			if start.Name.Local == "plist" {
				continue
			}
			return decodeXMLValue(dec, start, 0)
		}
	}
}

func decodeXMLValue(dec *xml.Decoder, start xml.StartElement, depth int) (interface{}, error) {
	if depth > 32 {
		return nil, errBadPlist
	}
	text := func() (string, error) {
		var s string
		err := dec.DecodeElement(&s, &start)
		return strings.TrimSpace(s), err
	}
	switch start.Name.Local {
	case "dict", "array":
		dict := map[string]interface{}{}
		var arr []interface{}
		var key *string
		for {
			tok, err := dec.Token()
			if err != nil {
				return nil, err
			}
			switch t := tok.(type) {
			case xml.EndElement:
				if start.Name.Local == "dict" {
					return dict, nil
				}
				return arr, nil
			case xml.StartElement:
				if start.Name.Local == "dict" && t.Name.Local == "key" {
					var k string
					if err := dec.DecodeElement(&k, &t); err != nil {
						return nil, err
					}
					key = &k
					continue
				}
				v, err := decodeXMLValue(dec, t, depth+1)
				if err != nil {
					return nil, err
				}
				if start.Name.Local == "array" {
					arr = append(arr, v)
				} else if key != nil {
					dict[*key] = v
					key = nil
				}
			}
		}
	case "string", "date":
		var s string
		err := dec.DecodeElement(&s, &start)
		return s, err
	case "integer":
		s, err := text()
		if err != nil {
			return nil, err
		}
		return strconv.ParseInt(s, 10, 64)
	case "real":
		s, err := text()
		if err != nil {
			return nil, err
		}
		return strconv.ParseFloat(s, 64)
	case "data":
		s, err := text()
		if err != nil {
			return nil, err
		}
		return base64.StdEncoding.DecodeString(strings.Join(strings.Fields(s), ""))
	case "true", "false":
		return start.Name.Local == "true", dec.Skip()
	}
	return nil, dec.Skip()
}

// plistString formats a scalar plist value as a string.
func plistString(v interface{}) string {
	switch v := v.(type) {
	case nil, map[string]interface{}, []interface{}, []byte:
		return ""
	case string:
		return v
	}
	return fmt.Sprint(v)
}

// plistFloat returns a numeric plist value as float64.
func plistFloat(v interface{}) float64 {
	switch v := v.(type) {
	case float64:
		return v
	case int64:
		return float64(v)
	}
	return 0
}
