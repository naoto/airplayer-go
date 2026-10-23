package airplay

import (
	"bufio"
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/crypto/curve25519"
)

func appendName(b []byte, labels ...string) []byte {
	for _, l := range labels {
		b = append(b, byte(len(l)))
		b = append(b, l...)
	}
	return append(b, 0)
}

func appendRR(b []byte, owner []byte, typ uint16, rdata []byte) []byte {
	b = append(b, owner...)
	b = append(b, byte(typ>>8), byte(typ), 0x80, 1, 0, 0, 0x11, 0x94)
	b = append(b, byte(len(rdata)>>8), byte(len(rdata)))
	return append(b, rdata...)
}

func TestRecordsParse(t *testing.T) {
	msg := make([]byte, 12)
	msg[2] = 0x84 // response, authoritative
	binary.BigEndian.PutUint16(msg[6:], 1)
	binary.BigEndian.PutUint16(msg[10:], 3)

	// PTR _airplay._tcp.local -> Living Room._airplay._tcp.local
	serviceOff := len(msg)
	ptr := appendName(nil, "Living Room")
	ptr = ptr[:len(ptr)-1]
	ptr = append(ptr, 0xC0, byte(serviceOff))
	msg = appendRR(msg, appendName(nil, serviceLabels...), typePTR, ptr)
	instance := []byte{0xC0, byte(len(msg) - len(ptr))}

	srv := []byte{0, 0, 0, 0, 0x1B, 0x58} // port 7000
	srv = appendName(srv, "apple-tv", "local")
	msg = appendRR(msg, instance, typeSRV, srv)

	txt := []byte{}
	for _, kv := range []string{"model=AppleTV2,1", "srcvers=130.14", "deviceid=AA:BB:CC:DD:EE:FF"} {
		txt = append(txt, byte(len(kv)))
		txt = append(txt, kv...)
	}
	msg = appendRR(msg, instance, typeTXT, txt)
	msg = appendRR(msg, appendName(nil, "Apple-TV", "local"), typeA, []byte{10, 0, 1, 2})

	rs := newRecords()
	if err := rs.parse(msg); err != nil {
		t.Fatal(err)
	}
	if q := rs.missing(); len(q) != 0 {
		t.Errorf("missing() = %d queries, want 0", len(q))
	}
	devices := rs.devices()
	if len(devices) != 1 {
		t.Fatalf("devices() = %v, want 1 device", devices)
	}
	d := devices[0]
	if d.Name != "Living Room" || d.Addr() != "10.0.1.2:7000" || d.TXT["model"] != "AppleTV2,1" {
		t.Errorf("device = %+v", d)
	}
	if d.ID() != "aa:bb:cc:dd:ee:ff" {
		t.Errorf("ID() = %q", d.ID())
	}
}

func TestRecordsMissing(t *testing.T) {
	rs := newRecords()
	rs.addInstance([]string{"TV", "_airplay", "_tcp", "local"})
	if q := rs.missing(); len(q) != 1 || binary.BigEndian.Uint16(q[0][len(q[0])-4:]) != typeSRV {
		t.Errorf("missing() = %v, want one SRV query", q)
	}
	if len(rs.devices()) != 0 {
		t.Error("unresolved instance must not be returned as a device")
	}
}

func TestReadNameLoop(t *testing.T) {
	msg := []byte{0xC0, 0}
	if _, _, err := readName(msg, 0); err == nil {
		t.Error("expected error for pointer loop")
	}
}

func TestTLV(t *testing.T) {
	long := bytes.Repeat([]byte{7}, 600)
	b := encodeTLV(tlvItem{tlvState, []byte{1}}, tlvItem{tlvPublicKey, long}, tlvItem{tlvProof, nil})
	m, err := decodeTLV(b)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(m[tlvState], []byte{1}) || !bytes.Equal(m[tlvPublicKey], long) {
		t.Errorf("decodeTLV() = %v", m)
	}
	if v, ok := m[tlvProof]; !ok || len(v) != 0 {
		t.Errorf("empty item = %v, %v", v, ok)
	}
	if _, err := decodeTLV([]byte{1, 5, 0}); err == nil {
		t.Error("expected error for truncated TLV")
	}
}

func TestXMLPlist(t *testing.T) {
	plist := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>duration</key>
	<real>120.5</real>
	<key>nested</key>
	<dict><key>width</key><integer>1</integer></dict>
	<key>list</key>
	<array><string>a</string><integer>2</integer></array>
	<key>model</key>
	<string>AppleTV2,1</string>
	<key>width</key>
	<integer>1280</integer>
	<key>readyToPlay</key>
	<true/>
	<key>blob</key>
	<data>
	AQID
	</data>
</dict>
</plist>`
	m, err := decodePlist([]byte(plist))
	if err != nil {
		t.Fatal(err)
	}
	if m["duration"] != 120.5 || m["model"] != "AppleTV2,1" || m["width"] != int64(1280) || m["readyToPlay"] != true {
		t.Errorf("decodePlist() = %v", m)
	}
	if n, _ := m["nested"].(map[string]interface{}); n["width"] != int64(1) {
		t.Errorf("nested = %v", m["nested"])
	}
	if l, _ := m["list"].([]interface{}); len(l) != 2 || l[0] != "a" || l[1] != int64(2) {
		t.Errorf("list = %v", m["list"])
	}
	if !bytes.Equal(m["blob"].([]byte), []byte{1, 2, 3}) {
		t.Errorf("blob = %v", m["blob"])
	}
}

func TestBinaryPlistRoundTrip(t *testing.T) {
	in := map[string]interface{}{
		"Content-Location": "http://10.0.1.6:7070/007 SKYFALL.mp4",
		"Start-Position":   0.25,
		"count":            int64(70000),
		"small":            int64(3),
		"title":            "攻殻機動隊",
		"long":             strings.Repeat("x", 300),
		"ok":               true,
		"data":             []byte{1, 2, 3},
		"list":             []interface{}{int64(1), "two", false},
		"dict":             map[string]interface{}{"a": int64(1)},
	}
	b, err := encodeBinaryPlist(in)
	if err != nil {
		t.Fatal(err)
	}
	out, err := decodePlist(b)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range in {
		switch v := v.(type) {
		case []byte:
			if !bytes.Equal(out[k].([]byte), v) {
				t.Errorf("%s = %v, want %v", k, out[k], v)
			}
		case []interface{}, map[string]interface{}:
			if plistString(out[k]) != "" || out[k] == nil {
				t.Errorf("%s = %v", k, out[k])
			}
		default:
			if out[k] != v {
				t.Errorf("%s = %v, want %v", k, out[k], v)
			}
		}
	}
	if l := out["list"].([]interface{}); len(l) != 3 || l[1] != "two" {
		t.Errorf("list = %v", l)
	}

	if _, err := decodePlist(append([]byte("bplist00"), make([]byte, 32)...)); err == nil {
		t.Error("expected error for malformed plist")
	}
}

func TestSecureConn(t *testing.T) {
	keyA, keyB := make([]byte, 32), make([]byte, 32)
	rand.Read(keyA)
	rand.Read(keyB)
	var wire bytes.Buffer
	w, _ := newSecureConn(&wire, sessionKeys{write: keyA, read: keyB})
	r, _ := newSecureConn(&wire, sessionKeys{write: keyB, read: keyA})

	msg := bytes.Repeat([]byte("airplay"), 500) // spans several frames
	if _, err := w.Write(msg); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("second")); err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, append(msg, "second"...)) {
		t.Error("decrypted data mismatch")
	}
}

// fakeSRPServer is the accessory side of SRP.
type fakeSRPServer struct {
	salt, B []byte
	b, v    *big.Int
}

func newFakeSRPServer(pin string) *fakeSRPServer {
	salt := make([]byte, 16)
	rand.Read(salt)
	x := new(big.Int).SetBytes(srpHash(salt, srpHash([]byte(srpUsername+":"+pin))))
	v := new(big.Int).Exp(srpG, x, srpN)
	b, _ := rand.Int(rand.Reader, srpN)
	k := new(big.Int).SetBytes(srpHash(srpN.Bytes(), srpPad(srpG.Bytes())))
	B := new(big.Int).Mul(k, v)
	B.Add(B, new(big.Int).Exp(srpG, b, srpN))
	B.Mod(B, srpN)
	return &fakeSRPServer{salt: salt, B: srpPad(B.Bytes()), b: b, v: v}
}

// verify checks the client proof and returns the session key and M2.
func (s *fakeSRPServer) verify(A, M1 []byte) (K, M2 []byte, ok bool) {
	u := new(big.Int).SetBytes(srpHash(srpPad(A), s.B))
	S := new(big.Int).Exp(s.v, u, srpN)
	S.Mul(S, new(big.Int).SetBytes(A))
	S.Exp(S, s.b, srpN)
	K = srpHash(S.Bytes())

	hN, hG := srpHash(srpN.Bytes()), srpHash(srpG.Bytes())
	for i := range hN {
		hN[i] ^= hG[i]
	}
	want := srpHash(hN, srpHash([]byte(srpUsername)), s.salt, A, s.B, K)
	return K, srpHash(A, M1, K), bytes.Equal(want, M1)
}

func TestSRP(t *testing.T) {
	// N must be the RFC 5054 3072-bit safe prime.
	q := new(big.Int).Rsh(srpN, 1)
	if srpN.BitLen() != 3072 || !srpN.ProbablyPrime(20) || !q.ProbablyPrime(20) {
		t.Fatal("srpN is not a 3072-bit safe prime")
	}

	server := newFakeSRPServer("1234")
	client, err := newSRPClient("1234", server.salt, server.B)
	if err != nil {
		t.Fatal(err)
	}
	K, M2, ok := server.verify(client.A, client.M1)
	if !ok || !bytes.Equal(K, client.K) || !client.verify(M2) {
		t.Error("SRP handshake failed with the right PIN")
	}

	wrong, _ := newSRPClient("0000", server.salt, server.B)
	if _, _, ok := server.verify(wrong.A, wrong.M1); ok {
		t.Error("SRP handshake succeeded with a wrong PIN")
	}
}

// fakeDevice is an AirPlay receiver implementing HomeKit pairing.
type fakeDevice struct {
	t           *testing.T
	ln          net.Listener
	pin         string
	id          string
	key         ed25519.PrivateKey
	clients     map[string]ed25519.PublicKey
	transient   bool // supports transient pairing
	requirePair bool // rejects unauthenticated requests
	played      chan map[string]interface{}
}

func newFakeDevice(t *testing.T) *fakeDevice {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	d := &fakeDevice{
		t: t, ln: ln, pin: "4321", id: "AA:BB:CC:DD:EE:FF", key: key,
		clients: map[string]ed25519.PublicKey{},
		played:  make(chan map[string]interface{}, 1),
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go d.serve(conn)
		}
	}()
	return d
}

func (d *fakeDevice) device() Device {
	addr := d.ln.Addr().(*net.TCPAddr)
	return Device{Name: "Fake TV", Host: addr.IP.String(), Port: addr.Port, TXT: map[string]string{"srcvers": "770.8.1"}}
}

type fakeSession struct {
	srp      *fakeSRPServer
	srpKey   []byte
	verifyX  []byte // X25519 private key
	verifyPK []byte
	clientPK []byte
	shared   []byte
	authed   bool
}

func (d *fakeDevice) serve(conn net.Conn) {
	defer conn.Close()
	var r io.Reader = conn
	var w io.Writer = conn
	br := bufio.NewReader(r)
	s := &fakeSession{}
	for {
		req, err := http.ReadRequest(br)
		if err != nil {
			return
		}
		body, _ := io.ReadAll(req.Body)
		status, resp, keys := d.handle(s, req, body)

		var out bytes.Buffer
		out.WriteString("HTTP/1.1 " + strconv.Itoa(status) + " " + http.StatusText(status) + "\r\n")
		out.WriteString("Content-Length: " + strconv.Itoa(len(resp)) + "\r\n\r\n")
		out.Write(resp)
		if _, err := w.Write(out.Bytes()); err != nil {
			return
		}
		if keys != nil {
			// The device reads with the client's write key.
			sc, _ := newSecureConn(conn, sessionKeys{write: keys.read, read: keys.write})
			w = sc
			br = bufio.NewReader(sc)
			s.authed = true
		}
	}
}

func tlvResp(items ...tlvItem) []byte { return encodeTLV(items...) }

func (d *fakeDevice) handle(s *fakeSession, req *http.Request, body []byte) (int, []byte, *sessionKeys) {
	tlv, _ := decodeTLV(body)
	state := byte(0)
	if v := tlv[tlvState]; len(v) == 1 {
		state = v[0]
	}
	hkp := req.Header.Get("X-Apple-HKP")

	switch req.URL.Path {
	case "/pair-pin-start":
		return 200, nil, nil

	case "/pair-setup":
		if hkp == "4" && !d.transient {
			return 470, nil, nil
		}
		switch state {
		case 1:
			pin := d.pin
			if hkp == "4" {
				pin = transientPIN
			}
			s.srp = newFakeSRPServer(pin)
			return 200, tlvResp(tlvItem{tlvState, []byte{2}}, tlvItem{tlvSalt, s.srp.salt}, tlvItem{tlvPublicKey, s.srp.B}), nil
		case 3:
			K, M2, ok := s.srp.verify(tlv[tlvPublicKey], tlv[tlvProof])
			if !ok {
				return 200, tlvResp(tlvItem{tlvState, []byte{4}}, tlvItem{tlvError, []byte{2}}), nil
			}
			s.srpKey = K
			resp := tlvResp(tlvItem{tlvState, []byte{4}}, tlvItem{tlvProof, M2})
			if hkp == "4" {
				keys := controlKeys(K)
				return 200, resp, &keys
			}
			return 200, resp, nil
		case 5:
			encKey := hkdfSHA512(s.srpKey, "Pair-Setup-Encrypt-Salt", "Pair-Setup-Encrypt-Info", 32)
			plain, err := open(encKey, "PS-Msg05", tlv[tlvEncryptedData])
			if err != nil {
				d.t.Error(err)
				return 500, nil, nil
			}
			c, _ := decodeTLV(plain)
			x := hkdfSHA512(s.srpKey, "Pair-Setup-Controller-Sign-Salt", "Pair-Setup-Controller-Sign-Info", 32)
			if !ed25519.Verify(c[tlvPublicKey], concat(x, c[tlvIdentifier], c[tlvPublicKey]), c[tlvSignature]) {
				d.t.Error("bad controller signature")
				return 500, nil, nil
			}
			d.clients[string(c[tlvIdentifier])] = c[tlvPublicKey]

			pub := d.key.Public().(ed25519.PublicKey)
			ax := hkdfSHA512(s.srpKey, "Pair-Setup-Accessory-Sign-Salt", "Pair-Setup-Accessory-Sign-Info", 32)
			sub := encodeTLV(
				tlvItem{tlvIdentifier, []byte(d.id)},
				tlvItem{tlvPublicKey, pub},
				tlvItem{tlvSignature, ed25519.Sign(d.key, concat(ax, []byte(d.id), pub))},
			)
			sealed, _ := seal(encKey, "PS-Msg06", sub)
			return 200, tlvResp(tlvItem{tlvState, []byte{6}}, tlvItem{tlvEncryptedData, sealed}), nil
		}

	case "/pair-verify":
		switch state {
		case 1:
			s.verifyX = make([]byte, 32)
			rand.Read(s.verifyX)
			s.verifyPK, _ = curve25519.X25519(s.verifyX, curve25519.Basepoint)
			s.clientPK = tlv[tlvPublicKey]
			s.shared, _ = curve25519.X25519(s.verifyX, s.clientPK)
			encKey := hkdfSHA512(s.shared, "Pair-Verify-Encrypt-Salt", "Pair-Verify-Encrypt-Info", 32)
			sig := ed25519.Sign(d.key, concat(s.verifyPK, []byte(d.id), s.clientPK))
			sealed, _ := seal(encKey, "PV-Msg02", encodeTLV(tlvItem{tlvIdentifier, []byte(d.id)}, tlvItem{tlvSignature, sig}))
			return 200, tlvResp(tlvItem{tlvState, []byte{2}}, tlvItem{tlvPublicKey, s.verifyPK}, tlvItem{tlvEncryptedData, sealed}), nil
		case 3:
			encKey := hkdfSHA512(s.shared, "Pair-Verify-Encrypt-Salt", "Pair-Verify-Encrypt-Info", 32)
			plain, err := open(encKey, "PV-Msg03", tlv[tlvEncryptedData])
			if err != nil {
				return 200, tlvResp(tlvItem{tlvState, []byte{4}}, tlvItem{tlvError, []byte{2}}), nil
			}
			c, _ := decodeTLV(plain)
			pk, ok := d.clients[string(c[tlvIdentifier])]
			if !ok || !ed25519.Verify(pk, concat(s.clientPK, c[tlvIdentifier], s.verifyPK), c[tlvSignature]) {
				return 200, tlvResp(tlvItem{tlvState, []byte{4}}, tlvItem{tlvError, []byte{2}}), nil
			}
			keys := controlKeys(s.shared)
			return 200, tlvResp(tlvItem{tlvState, []byte{4}}), &keys
		}
	}

	if d.requirePair && !s.authed {
		return 403, nil, nil
	}
	switch req.URL.Path {
	case "/play":
		if req.Header.Get("Content-Type") != "application/x-apple-binary-plist" {
			d.t.Errorf("play content type = %q", req.Header.Get("Content-Type"))
		}
		m, err := decodePlist(body)
		if err != nil {
			d.t.Error(err)
		}
		d.played <- m
		return 200, nil, nil
	case "/playback-info":
		b, _ := encodeBinaryPlist(map[string]interface{}{
			"duration": 120.5, "position": 30.25, "readyToPlay": true,
		})
		return 200, b, nil
	case "/server-info":
		return 200, []byte(`<plist><dict><key>width</key><integer>1920</integer><key>height</key><integer>1080</integer></dict></plist>`), nil
	case "/stop":
		return 200, nil, nil
	}
	return 404, nil, nil
}

func checkSession(t *testing.T, d *fakeDevice, c *Client) {
	t.Helper()
	if err := c.Play("http://example.com/a.mp4"); err != nil {
		t.Fatal(err)
	}
	if m := <-d.played; m["Content-Location"] != "http://example.com/a.mp4" || m["Start-Position"] != 0.0 {
		t.Errorf("play body = %v", m)
	}
	info, err := c.PlaybackInfo()
	if err != nil || info.Position != 30.25 || info.Duration != 120.5 || !info.ReadyToPlay || info.Stopped {
		t.Errorf("PlaybackInfo() = %+v, %v", info, err)
	}
	if i := c.Info(); i.Resolution != "1920x1080" || i.Version != "770.8.1" || i.Model != "unknown" {
		t.Errorf("Info() = %+v", i)
	}
	if err := c.Stop(); err != nil {
		t.Error(err)
	}
}

func TestConnectTransient(t *testing.T) {
	d := newFakeDevice(t)
	d.transient = true
	d.requirePair = true
	c, err := Connect(d.device(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if !c.Encrypted {
		t.Error("session is not encrypted")
	}
	checkSession(t, d, c)
}

func TestConnectLegacy(t *testing.T) {
	d := newFakeDevice(t)
	c, err := Connect(d.device(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if c.Encrypted {
		t.Error("legacy session must not be encrypted")
	}
	checkSession(t, d, c)
}

func TestPairingRequired(t *testing.T) {
	d := newFakeDevice(t)
	d.requirePair = true
	c, err := Connect(d.device(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Play("http://example.com/a.mp4"); !errors.Is(err, ErrPairingRequired) {
		t.Errorf("Play() = %v, want ErrPairingRequired", err)
	}
}

func TestPairAndVerify(t *testing.T) {
	d := newFakeDevice(t)
	d.requirePair = true

	if _, err := Pair(d.device(), "test", func() (string, error) { return "0000\n", nil }); err == nil {
		t.Error("pairing with a wrong PIN succeeded")
	}

	creds, err := Pair(d.device(), "test", func() (string, error) { return d.pin + "\n", nil })
	if err != nil {
		t.Fatal(err)
	}
	if creds.DeviceID != d.id {
		t.Errorf("DeviceID = %q", creds.DeviceID)
	}

	c, err := Connect(d.device(), creds)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if !c.Encrypted {
		t.Error("session is not encrypted")
	}
	checkSession(t, d, c)

	// Credentials that the device does not know are rejected.
	other := *creds
	other.ClientID = "unknown"
	if _, err := Connect(d.device(), &other); err == nil {
		t.Error("Connect() with unknown credentials succeeded")
	}
}
