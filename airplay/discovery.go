// Package airplay implements a minimal AirPlay video client:
// device discovery over mDNS and the HTTP based playback protocol.
package airplay

import (
	"encoding/binary"
	"errors"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ErrNoDevices is returned by Browse when no AirPlay device answers.
var ErrNoDevices = errors.New("airplay: no devices found")

var errMalformed = errors.New("airplay: malformed mDNS message")

const (
	mdnsAddr = "224.0.0.251:5353"

	typeA   = 1
	typePTR = 12
	typeTXT = 16
	typeSRV = 33
)

var serviceLabels = []string{"_airplay", "_tcp", "local"}

// Device is an AirPlay receiver found on the local network.
type Device struct {
	Name string
	Host string
	Port int
	TXT  map[string]string
}

// ID returns a stable identifier of the device, used to look up its
// pairing credentials.
func (d Device) ID() string {
	return strings.ToLower(firstNonEmpty(d.TXT["deviceid"], d.TXT["pi"], d.Name))
}

// Addr returns the host:port of the AirPlay HTTP service.
func (d Device) Addr() string {
	return net.JoinHostPort(d.Host, strconv.Itoa(d.Port))
}

// Browse discovers AirPlay devices on the local network, waiting up to
// timeout for answers. Devices are sorted by name.
func Browse(timeout time.Duration) ([]Device, error) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{})
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	dst, err := net.ResolveUDPAddr("udp4", mdnsAddr)
	if err != nil {
		return nil, err
	}

	rs := newRecords()
	if _, err := conn.WriteTo(buildQuery(serviceLabels, typePTR), dst); err != nil {
		return nil, err
	}
	collect(conn, rs, timeout)

	// Responders usually include SRV, TXT and A records as additional
	// records, but ask explicitly for whatever is still missing.
	for i := 0; i < 2; i++ {
		queries := rs.missing()
		if len(queries) == 0 {
			break
		}
		for _, q := range queries {
			conn.WriteTo(q, dst)
		}
		collect(conn, rs, timeout/2)
	}

	devices := rs.devices()
	if len(devices) == 0 {
		return nil, ErrNoDevices
	}
	return devices, nil
}

func collect(conn *net.UDPConn, rs *records, timeout time.Duration) {
	conn.SetReadDeadline(time.Now().Add(timeout))
	buf := make([]byte, 9000)
	for {
		n, _, err := conn.ReadFrom(buf)
		if err != nil {
			return
		}
		rs.parse(buf[:n])
	}
}

func buildQuery(labels []string, qtype uint16) []byte {
	b := make([]byte, 12)
	binary.BigEndian.PutUint16(b[4:], 1) // QDCOUNT
	for _, l := range labels {
		b = append(b, byte(len(l)))
		b = append(b, l...)
	}
	b = append(b, 0)
	return append(b, byte(qtype>>8), byte(qtype), 0, 1) // class IN
}

type srvRecord struct {
	target []string
	port   int
}

type records struct {
	instances [][]string
	srv       map[string]srvRecord
	txt       map[string]map[string]string
	a         map[string]net.IP
}

func newRecords() *records {
	return &records{
		srv: map[string]srvRecord{},
		txt: map[string]map[string]string{},
		a:   map[string]net.IP{},
	}
}

func key(labels []string) string {
	return strings.ToLower(strings.Join(labels, "."))
}

func (rs *records) addInstance(labels []string) {
	k := key(labels)
	for _, in := range rs.instances {
		if key(in) == k {
			return
		}
	}
	rs.instances = append(rs.instances, labels)
}

func (rs *records) parse(msg []byte) error {
	if len(msg) < 12 {
		return errMalformed
	}
	if msg[2]&0x80 == 0 { // not a response
		return nil
	}
	qd := int(binary.BigEndian.Uint16(msg[4:]))
	rr := int(binary.BigEndian.Uint16(msg[6:])) +
		int(binary.BigEndian.Uint16(msg[8:])) +
		int(binary.BigEndian.Uint16(msg[10:]))

	off := 12
	for i := 0; i < qd; i++ {
		_, n, err := readName(msg, off)
		if err != nil {
			return err
		}
		off = n + 4
	}

	for i := 0; i < rr; i++ {
		owner, n, err := readName(msg, off)
		if err != nil {
			return err
		}
		off = n
		if off+10 > len(msg) {
			return errMalformed
		}
		typ := binary.BigEndian.Uint16(msg[off:])
		rdlen := int(binary.BigEndian.Uint16(msg[off+8:]))
		off += 10
		if off+rdlen > len(msg) {
			return errMalformed
		}
		rdata := msg[off : off+rdlen]

		switch typ {
		case typePTR:
			if key(owner) == key(serviceLabels) {
				target, _, err := readName(msg, off)
				if err != nil {
					return err
				}
				rs.addInstance(target)
			}
		case typeSRV:
			if rdlen < 7 {
				return errMalformed
			}
			target, _, err := readName(msg, off+6)
			if err != nil {
				return err
			}
			rs.srv[key(owner)] = srvRecord{target: target, port: int(binary.BigEndian.Uint16(rdata[4:]))}
		case typeTXT:
			rs.txt[key(owner)] = parseTXT(rdata)
		case typeA:
			if rdlen == 4 {
				rs.a[key(owner)] = net.IP(append([]byte(nil), rdata...))
			}
		}
		off += rdlen
	}
	return nil
}

// missing returns queries for the records still needed to resolve every
// discovered instance.
func (rs *records) missing() [][]byte {
	var qs [][]byte
	for _, in := range rs.instances {
		srv, ok := rs.srv[key(in)]
		if !ok {
			qs = append(qs, buildQuery(in, typeSRV))
			continue
		}
		if _, ok := rs.a[key(srv.target)]; !ok {
			qs = append(qs, buildQuery(srv.target, typeA))
		}
	}
	return qs
}

func (rs *records) devices() []Device {
	var devices []Device
	for _, in := range rs.instances {
		srv, ok := rs.srv[key(in)]
		if !ok {
			continue
		}
		ip, ok := rs.a[key(srv.target)]
		if !ok {
			continue
		}
		devices = append(devices, Device{
			Name: in[0],
			Host: ip.String(),
			Port: srv.port,
			TXT:  rs.txt[key(in)],
		})
	}
	sort.Slice(devices, func(i, j int) bool { return devices[i].Name < devices[j].Name })
	return devices
}

func readName(msg []byte, off int) ([]string, int, error) {
	var labels []string
	end := -1
	for jumps := 0; ; {
		if off >= len(msg) {
			return nil, 0, errMalformed
		}
		l := int(msg[off])
		switch {
		case l == 0:
			if end < 0 {
				end = off + 1
			}
			return labels, end, nil
		case l&0xC0 == 0xC0:
			if off+1 >= len(msg) {
				return nil, 0, errMalformed
			}
			if end < 0 {
				end = off + 2
			}
			if jumps++; jumps > 32 {
				return nil, 0, errMalformed
			}
			off = int(binary.BigEndian.Uint16(msg[off:]) & 0x3FFF)
		case l&0xC0 != 0:
			return nil, 0, errMalformed
		default:
			off++
			if off+l > len(msg) {
				return nil, 0, errMalformed
			}
			labels = append(labels, string(msg[off:off+l]))
			off += l
		}
	}
}

func parseTXT(rdata []byte) map[string]string {
	txt := map[string]string{}
	for len(rdata) > 0 {
		l := int(rdata[0])
		if 1+l > len(rdata) {
			break
		}
		entry := string(rdata[1 : 1+l])
		rdata = rdata[1+l:]
		if k, v, ok := cut(entry, "="); ok {
			txt[strings.ToLower(k)] = v
		} else if entry != "" {
			txt[strings.ToLower(entry)] = ""
		}
	}
	return txt
}

// cut is strings.Cut, which needs Go 1.18.
func cut(s, sep string) (before, after string, found bool) {
	if i := strings.Index(s, sep); i >= 0 {
		return s[:i], s[i+len(sep):], true
	}
	return s, "", false
}
