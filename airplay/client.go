package airplay

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"
)

const playRetries = 3

// Client controls video playback on a single AirPlay device.
//
// The device stops playback when the connection that issued /play is
// closed, so all requests share one persistent, possibly encrypted,
// connection.
type Client struct {
	Device    Device
	Encrypted bool
	conn      *httpConn
	sessionID string
}

// Info describes an AirPlay device.
type Info struct {
	Model      string
	Version    string
	Resolution string
}

// PlaybackInfo is the playback state reported by the device.
type PlaybackInfo struct {
	Position    float64
	Duration    float64
	ReadyToPlay bool
	// Stopped is true when the device is no longer playing anything.
	Stopped bool
}

// Connect opens a session with d. With credentials it authenticates by
// pair-verify; otherwise it tries transient pairing and falls back to an
// unauthenticated connection for older devices.
func Connect(d Device, creds *Credentials) (*Client, error) {
	h, err := dialHTTP(d.Addr())
	if err != nil {
		return nil, err
	}
	c := &Client{Device: d, conn: h, sessionID: newSessionID()}

	var keys sessionKeys
	if creds != nil {
		keys, err = pairVerify(h, creds)
		if err != nil {
			h.Close()
			return nil, fmt.Errorf("%w (pair again with `airplayer pair`)", err)
		}
	} else if keys, err = transientPair(h); err != nil {
		// The device does not support transient pairing. Reconnect, as a
		// failed pairing may leave the connection in an unknown state.
		h.Close()
		if c.conn, err = dialHTTP(d.Addr()); err != nil {
			return nil, err
		}
		return c, nil
	}

	if err := h.encrypt(keys); err != nil {
		h.Close()
		return nil, err
	}
	c.Encrypted = true
	return c, nil
}

// Pair pairs with d using the PIN shown on its screen. pin is called
// after the device displays the PIN; name is shown in the device's list
// of paired clients.
func Pair(d Device, name string, pin func() (string, error)) (*Credentials, error) {
	h, err := dialHTTP(d.Addr())
	if err != nil {
		return nil, err
	}
	defer h.Close()

	if err := startPairing(h); err != nil {
		return nil, err
	}
	code, err := pin()
	if err != nil {
		return nil, err
	}
	return pairSetup(h, strings.TrimSpace(code), name)
}

// LocalIP returns the local address used to talk to the device.
func (c *Client) LocalIP() net.IP {
	return c.conn.conn.LocalAddr().(*net.TCPAddr).IP
}

func (c *Client) request(method, path string, body []byte) (*response, error) {
	header := map[string]string{
		"User-Agent":         "MediaControl/1.0",
		"X-Apple-Session-ID": c.sessionID,
	}
	if body != nil {
		header["Content-Type"] = "application/x-apple-binary-plist"
	}
	resp, err := c.conn.do(method, path, header, body)
	if err != nil {
		return nil, err
	}
	switch resp.status {
	case http.StatusOK:
		return resp, nil
	case http.StatusUnauthorized, http.StatusForbidden, 470:
		return nil, fmt.Errorf("%w (%s %s: HTTP %d)", ErrPairingRequired, method, path, resp.status)
	}
	return resp, fmt.Errorf("airplay: %s %s: HTTP %d %s", method, path, resp.status, http.StatusText(resp.status))
}

// Play starts playing the video at mediaURL from the beginning.
func (c *Client) Play(mediaURL string) error {
	body, err := encodeBinaryPlist(map[string]interface{}{
		"Content-Location":   mediaURL,
		"Start-Position":     0.0,
		"X-Apple-Session-ID": c.sessionID,
	})
	if err != nil {
		return err
	}
	for i := 0; ; i++ {
		resp, err := c.request("POST", "/play", body)
		// Devices sometimes answer 500 while getting ready; retry.
		if err != nil && resp != nil && resp.status == http.StatusInternalServerError && i < playRetries {
			time.Sleep(time.Second)
			continue
		}
		return err
	}
}

// PlaybackInfo returns the current playback state.
func (c *Client) PlaybackInfo() (PlaybackInfo, error) {
	resp, err := c.request("GET", "/playback-info", nil)
	if err != nil {
		return PlaybackInfo{}, err
	}
	if len(resp.body) == 0 {
		return PlaybackInfo{Stopped: true}, nil
	}
	m, err := decodePlist(resp.body)
	if err != nil {
		return PlaybackInfo{}, err
	}
	if e, ok := m["error"]; ok {
		return PlaybackInfo{}, fmt.Errorf("airplay: playback error: %v", e)
	}
	ready, hasReady := m["readyToPlay"]
	readyToPlay := ready == true || plistFloat(ready) != 0
	return PlaybackInfo{
		Position:    plistFloat(m["position"]),
		Duration:    plistFloat(m["duration"]),
		ReadyToPlay: readyToPlay,
		Stopped:     !hasReady,
	}, nil
}

// Stop stops playback.
func (c *Client) Stop() error {
	_, err := c.request("POST", "/stop", nil)
	return err
}

// Info returns information about the device. Fields that cannot be
// determined are set to "unknown".
func (c *Client) Info() Info {
	info := Info{
		Model:   c.Device.TXT["model"],
		Version: firstNonEmpty(c.Device.TXT["osvers"], c.Device.TXT["srcvers"]),
	}
	if resp, err := c.request("GET", "/server-info", nil); err == nil {
		if si, err := decodePlist(resp.body); err == nil {
			info.Model = firstNonEmpty(plistString(si["model"]), info.Model)
			info.Version = firstNonEmpty(plistString(si["osBuildVersion"]), info.Version)
			if w, h := plistString(si["width"]), plistString(si["height"]); w != "" && h != "" {
				info.Resolution = w + "x" + h
			}
		}
	}
	info.Model = firstNonEmpty(info.Model, "unknown")
	info.Version = firstNonEmpty(info.Version, "unknown")
	info.Resolution = firstNonEmpty(info.Resolution, "unknown")
	return info
}

// Close closes the session.
func (c *Client) Close() error {
	return c.conn.Close()
}

func newSessionID() string {
	b := make([]byte, 16)
	rand.Read(b)
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	s := hex.EncodeToString(b)
	return strings.ToUpper(s[:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:])
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}
