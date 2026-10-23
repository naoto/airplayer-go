package airplay

import "errors"

// TLV8 types used by HomeKit (HAP) pairing.
const (
	tlvMethod        = 0x00
	tlvIdentifier    = 0x01
	tlvSalt          = 0x02
	tlvPublicKey     = 0x03
	tlvProof         = 0x04
	tlvEncryptedData = 0x05
	tlvState         = 0x06
	tlvError         = 0x07
	tlvBackOff       = 0x08
	tlvSignature     = 0x0A
	tlvName          = 0x11
	tlvFlags         = 0x13
)

type tlvItem struct {
	typ   byte
	value []byte
}

// encodeTLV encodes items in order, splitting values longer than 255
// bytes into consecutive fragments.
func encodeTLV(items ...tlvItem) []byte {
	var b []byte
	for _, it := range items {
		v := it.value
		for {
			n := len(v)
			if n > 255 {
				n = 255
			}
			b = append(b, it.typ, byte(n))
			b = append(b, v[:n]...)
			v = v[n:]
			if len(v) == 0 {
				break
			}
		}
	}
	return b
}

// decodeTLV decodes b, joining consecutive fragments of the same type.
func decodeTLV(b []byte) (map[byte][]byte, error) {
	m := map[byte][]byte{}
	last := -1
	for len(b) > 0 {
		if len(b) < 2 || len(b) < 2+int(b[1]) {
			return nil, errors.New("airplay: malformed TLV8")
		}
		typ, v := b[0], b[2:2+int(b[1])]
		if int(typ) == last {
			m[typ] = append(m[typ], v...)
		} else {
			m[typ] = append([]byte(nil), v...)
		}
		last = int(typ)
		b = b[2+len(v):]
	}
	return m, nil
}
