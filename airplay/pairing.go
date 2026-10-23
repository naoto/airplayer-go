package airplay

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/curve25519"
)

// transientPIN is the fixed PIN of HomeKit transient pairing.
const transientPIN = "3939"

// pairingFlagTransient requests a transient pairing in pair-setup M1.
const pairingFlagTransient = 0x10

// ErrPairingRequired means the device rejected the connection and must be
// paired first.
var ErrPairingRequired = errors.New("airplay: device requires pairing")

// Credentials are the long-term keys of a HomeKit pairing.
type Credentials struct {
	// ClientID is the pairing identifier of this client.
	ClientID string `json:"client_id"`
	// ClientKey is the Ed25519 private key seed of this client.
	ClientKey []byte `json:"client_key"`
	// DeviceID is the pairing identifier of the device.
	DeviceID string `json:"device_id"`
	// DeviceKey is the Ed25519 public key of the device.
	DeviceKey []byte `json:"device_key"`
}

func pairingHeaders(hkp string) map[string]string {
	return map[string]string{
		"User-Agent":   "AirPlay/320.20",
		"Connection":   "keep-alive",
		"X-Apple-HKP":  hkp,
		"Content-Type": "application/octet-stream",
	}
}

// pairingError reports a pairing failure for HTTP status or TLV errors.
type pairingError struct {
	step   string
	status int
	code   byte
}

func (e *pairingError) Error() string {
	if e.status != 0 {
		return fmt.Sprintf("airplay: %s: HTTP %d %s", e.step, e.status, http.StatusText(e.status))
	}
	switch e.code {
	case 0x02:
		return fmt.Sprintf("airplay: %s: authentication failed (wrong PIN?)", e.step)
	case 0x03:
		return fmt.Sprintf("airplay: %s: too many attempts, try again later", e.step)
	case 0x06:
		return fmt.Sprintf("airplay: %s: device is busy pairing with another client", e.step)
	}
	return fmt.Sprintf("airplay: %s: error 0x%02x", e.step, e.code)
}

func pairingStep(h *httpConn, path, hkp, step string, items ...tlvItem) (map[byte][]byte, error) {
	resp, err := h.do("POST", path, pairingHeaders(hkp), encodeTLV(items...))
	if err != nil {
		return nil, err
	}
	if resp.status != http.StatusOK {
		return nil, &pairingError{step: step, status: resp.status}
	}
	tlv, err := decodeTLV(resp.body)
	if err != nil {
		return nil, err
	}
	if e, ok := tlv[tlvError]; ok && len(e) > 0 {
		return nil, &pairingError{step: step, code: e[0]}
	}
	return tlv, nil
}

func seal(key []byte, nonce string, plain []byte) ([]byte, error) {
	aead, err := chacha20poly1305.New(key)
	if err != nil {
		return nil, err
	}
	return aead.Seal(nil, nonceString(nonce), plain, nil), nil
}

func open(key []byte, nonce string, sealed []byte) ([]byte, error) {
	aead, err := chacha20poly1305.New(key)
	if err != nil {
		return nil, err
	}
	plain, err := aead.Open(nil, nonceString(nonce), sealed, nil)
	if err != nil {
		return nil, errors.New("airplay: failed to decrypt pairing data")
	}
	return plain, nil
}

// srpExchange performs pair-setup M1 to M4 and returns the SRP session.
func srpExchange(h *httpConn, hkp, pin string, m1 ...tlvItem) (*srpClient, error) {
	m2, err := pairingStep(h, "/pair-setup", hkp, "pair-setup M1", m1...)
	if err != nil {
		return nil, err
	}
	srp, err := newSRPClient(pin, m2[tlvSalt], m2[tlvPublicKey])
	if err != nil {
		return nil, err
	}
	m4, err := pairingStep(h, "/pair-setup", hkp, "pair-setup M3",
		tlvItem{tlvState, []byte{3}},
		tlvItem{tlvPublicKey, srp.A},
		tlvItem{tlvProof, srp.M1},
	)
	if err != nil {
		return nil, err
	}
	if !srp.verify(m4[tlvProof]) {
		return nil, errors.New("airplay: pair-setup: device proof mismatch")
	}
	return srp, nil
}

// transientPair performs a HomeKit transient pairing, which needs no PIN
// from the user, and returns the session keys.
func transientPair(h *httpConn) (sessionKeys, error) {
	srp, err := srpExchange(h, "4", transientPIN,
		tlvItem{tlvMethod, []byte{0}},
		tlvItem{tlvState, []byte{1}},
		tlvItem{tlvFlags, []byte{pairingFlagTransient}},
	)
	if err != nil {
		return sessionKeys{}, err
	}
	return controlKeys(srp.K), nil
}

// startPairing asks the device to show a PIN on screen.
func startPairing(h *httpConn) error {
	resp, err := h.do("POST", "/pair-pin-start", pairingHeaders("3"), nil)
	if err != nil {
		return err
	}
	if resp.status != http.StatusOK {
		return &pairingError{step: "pair-pin-start", status: resp.status}
	}
	return nil
}

// pairSetup performs a HomeKit pair-setup with the PIN shown on the
// device and returns the long-term credentials.
func pairSetup(h *httpConn, pin, name string) (*Credentials, error) {
	srp, err := srpExchange(h, "3", pin,
		tlvItem{tlvMethod, []byte{0}},
		tlvItem{tlvState, []byte{1}},
	)
	if err != nil {
		return nil, err
	}

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	clientID := newSessionID()

	// M5: exchange long-term keys.
	encKey := hkdfSHA512(srp.K, "Pair-Setup-Encrypt-Salt", "Pair-Setup-Encrypt-Info", 32)
	x := hkdfSHA512(srp.K, "Pair-Setup-Controller-Sign-Salt", "Pair-Setup-Controller-Sign-Info", 32)
	sig := ed25519.Sign(priv, concat(x, []byte(clientID), pub))
	sub := encodeTLV(
		tlvItem{tlvIdentifier, []byte(clientID)},
		tlvItem{tlvPublicKey, pub},
		tlvItem{tlvSignature, sig},
		tlvItem{tlvName, []byte(name)},
	)
	sealed, err := seal(encKey, "PS-Msg05", sub)
	if err != nil {
		return nil, err
	}
	m6, err := pairingStep(h, "/pair-setup", "3", "pair-setup M5",
		tlvItem{tlvState, []byte{5}},
		tlvItem{tlvEncryptedData, sealed},
	)
	if err != nil {
		return nil, err
	}

	// M6: verify the device's long-term key.
	plain, err := open(encKey, "PS-Msg06", m6[tlvEncryptedData])
	if err != nil {
		return nil, err
	}
	dev, err := decodeTLV(plain)
	if err != nil {
		return nil, err
	}
	devID, devKey := dev[tlvIdentifier], dev[tlvPublicKey]
	if len(devKey) != ed25519.PublicKeySize {
		return nil, errors.New("airplay: pair-setup: invalid device key")
	}
	ax := hkdfSHA512(srp.K, "Pair-Setup-Accessory-Sign-Salt", "Pair-Setup-Accessory-Sign-Info", 32)
	if !ed25519.Verify(devKey, concat(ax, devID, devKey), dev[tlvSignature]) {
		return nil, errors.New("airplay: pair-setup: invalid device signature")
	}

	return &Credentials{
		ClientID:  clientID,
		ClientKey: priv.Seed(),
		DeviceID:  string(devID),
		DeviceKey: devKey,
	}, nil
}

// pairVerify authenticates with stored credentials and returns the
// session keys.
func pairVerify(h *httpConn, c *Credentials) (sessionKeys, error) {
	if len(c.ClientKey) != ed25519.SeedSize || len(c.DeviceKey) != ed25519.PublicKeySize {
		return sessionKeys{}, errors.New("airplay: invalid credentials")
	}

	secret := make([]byte, curve25519.ScalarSize)
	if _, err := rand.Read(secret); err != nil {
		return sessionKeys{}, err
	}
	pub, err := curve25519.X25519(secret, curve25519.Basepoint)
	if err != nil {
		return sessionKeys{}, err
	}

	m2, err := pairingStep(h, "/pair-verify", "3", "pair-verify M1",
		tlvItem{tlvState, []byte{1}},
		tlvItem{tlvPublicKey, pub},
	)
	if err != nil {
		return sessionKeys{}, err
	}
	devPub := m2[tlvPublicKey]
	shared, err := curve25519.X25519(secret, devPub)
	if err != nil {
		return sessionKeys{}, err
	}
	encKey := hkdfSHA512(shared, "Pair-Verify-Encrypt-Salt", "Pair-Verify-Encrypt-Info", 32)

	plain, err := open(encKey, "PV-Msg02", m2[tlvEncryptedData])
	if err != nil {
		return sessionKeys{}, err
	}
	dev, err := decodeTLV(plain)
	if err != nil {
		return sessionKeys{}, err
	}
	if !strings.EqualFold(string(dev[tlvIdentifier]), c.DeviceID) {
		return sessionKeys{}, errors.New("airplay: pair-verify: credentials belong to another device")
	}
	if !ed25519.Verify(c.DeviceKey, concat(devPub, dev[tlvIdentifier], pub), dev[tlvSignature]) {
		return sessionKeys{}, errors.New("airplay: pair-verify: invalid device signature")
	}

	priv := ed25519.NewKeyFromSeed(c.ClientKey)
	sig := ed25519.Sign(priv, concat(pub, []byte(c.ClientID), devPub))
	sealed, err := seal(encKey, "PV-Msg03", encodeTLV(
		tlvItem{tlvIdentifier, []byte(c.ClientID)},
		tlvItem{tlvSignature, sig},
	))
	if err != nil {
		return sessionKeys{}, err
	}
	if _, err := pairingStep(h, "/pair-verify", "3", "pair-verify M3",
		tlvItem{tlvState, []byte{3}},
		tlvItem{tlvEncryptedData, sealed},
	); err != nil {
		return sessionKeys{}, err
	}
	return controlKeys(shared), nil
}

func concat(parts ...[]byte) []byte {
	return bytes.Join(parts, nil)
}
