package airplay

import (
	"bufio"
	"bytes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha512"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"golang.org/x/crypto/chacha20poly1305"
)

const (
	frameSize      = 1024
	requestTimeout = 15 * time.Second
)

// sessionKeys are the ChaCha20-Poly1305 keys of an encrypted session.
type sessionKeys struct {
	write, read []byte
}

// controlKeys derives the HTTP control channel keys from a shared secret.
func controlKeys(secret []byte) sessionKeys {
	return sessionKeys{
		write: hkdfSHA512(secret, "Control-Salt", "Control-Write-Encryption-Key", 32),
		read:  hkdfSHA512(secret, "Control-Salt", "Control-Read-Encryption-Key", 32),
	}
}

func hkdfSHA512(secret []byte, salt, info string, n int) []byte {
	ext := hmac.New(sha512.New, []byte(salt))
	ext.Write(secret)
	prk := ext.Sum(nil)

	var out, t []byte
	for i := byte(1); len(out) < n; i++ {
		h := hmac.New(sha512.New, prk)
		h.Write(t)
		h.Write([]byte(info))
		h.Write([]byte{i})
		t = h.Sum(nil)
		out = append(out, t...)
	}
	return out[:n]
}

// nonce64 returns a 12-byte nonce holding counter as 64-bit little endian
// after 4 zero bytes.
func nonce64(counter uint64) []byte {
	n := make([]byte, chacha20poly1305.NonceSize)
	binary.LittleEndian.PutUint64(n[4:], counter)
	return n
}

// nonceString returns a 12-byte nonce for the pairing messages
// ("PS-Msg05" etc.).
func nonceString(s string) []byte {
	return append(make([]byte, 4), s...)
}

// secureConn frames data as HAP encrypted blocks: a 2-byte little endian
// length, used as additional data, followed by the sealed payload.
type secureConn struct {
	rw       io.ReadWriter
	enc, dec cipher.AEAD
	encCount uint64
	decCount uint64
	pending  []byte
}

func newSecureConn(rw io.ReadWriter, keys sessionKeys) (*secureConn, error) {
	enc, err := chacha20poly1305.New(keys.write)
	if err != nil {
		return nil, err
	}
	dec, err := chacha20poly1305.New(keys.read)
	if err != nil {
		return nil, err
	}
	return &secureConn{rw: rw, enc: enc, dec: dec}, nil
}

func (s *secureConn) Write(p []byte) (int, error) {
	written := 0
	for len(p) > 0 {
		chunk := p
		if len(chunk) > frameSize {
			chunk = chunk[:frameSize]
		}
		hdr := make([]byte, 2)
		binary.LittleEndian.PutUint16(hdr, uint16(len(chunk)))
		frame := s.enc.Seal(append([]byte(nil), hdr...), nonce64(s.encCount), chunk, hdr)
		s.encCount++
		if _, err := s.rw.Write(frame); err != nil {
			return written, err
		}
		written += len(chunk)
		p = p[len(chunk):]
	}
	return written, nil
}

func (s *secureConn) Read(p []byte) (int, error) {
	if len(s.pending) == 0 {
		hdr := make([]byte, 2)
		if _, err := io.ReadFull(s.rw, hdr); err != nil {
			return 0, err
		}
		n := int(binary.LittleEndian.Uint16(hdr))
		if n > frameSize {
			return 0, fmt.Errorf("airplay: encrypted frame too large (%d bytes)", n)
		}
		sealed := make([]byte, n+s.dec.Overhead())
		if _, err := io.ReadFull(s.rw, sealed); err != nil {
			return 0, err
		}
		plain, err := s.dec.Open(sealed[:0], nonce64(s.decCount), sealed, hdr)
		if err != nil {
			return 0, fmt.Errorf("airplay: decrypt frame: %w", err)
		}
		s.decCount++
		s.pending = plain
	}
	n := copy(p, s.pending)
	s.pending = s.pending[n:]
	return n, nil
}

// httpConn sends HTTP/1.1 requests one at a time over a single
// connection, optionally encrypted.
type httpConn struct {
	conn net.Conn
	host string
	w    io.Writer
	r    *bufio.Reader
}

type response struct {
	status int
	header http.Header
	body   []byte
}

func dialHTTP(addr string) (*httpConn, error) {
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return nil, err
	}
	return &httpConn{conn: conn, host: addr, w: conn, r: bufio.NewReader(conn)}, nil
}

func (h *httpConn) encrypt(keys sessionKeys) error {
	sc, err := newSecureConn(h.conn, keys)
	if err != nil {
		return err
	}
	h.w = sc
	h.r = bufio.NewReader(sc)
	return nil
}

func (h *httpConn) do(method, path string, header map[string]string, body []byte) (*response, error) {
	req, err := http.NewRequest(method, "http://"+h.host+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}

	h.conn.SetDeadline(time.Now().Add(requestTimeout))
	defer h.conn.SetDeadline(time.Time{})

	bw := bufio.NewWriterSize(h.w, frameSize)
	if err := req.Write(bw); err != nil {
		return nil, err
	}
	if err := bw.Flush(); err != nil {
		return nil, err
	}

	resp, err := http.ReadResponse(h.r, req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return &response{status: resp.StatusCode, header: resp.Header, body: b}, nil
}

func (h *httpConn) Close() error {
	return h.conn.Close()
}
