package airplay

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha512"
	"errors"
	"math/big"
)

// SRP-6a with the RFC 5054 3072-bit group and SHA-512, as used by
// HomeKit pair-setup.

const srpUsername = "Pair-Setup"

var (
	srpN, _ = new(big.Int).SetString(""+
		"FFFFFFFFFFFFFFFFC90FDAA22168C234C4C6628B80DC1CD129024E088A67CC74"+
		"020BBEA63B139B22514A08798E3404DDEF9519B3CD3A431B302B0A6DF25F1437"+
		"4FE1356D6D51C245E485B576625E7EC6F44C42E9A637ED6B0BFF5CB6F406B7ED"+
		"EE386BFB5A899FA5AE9F24117C4B1FE649286651ECE45B3DC2007CB8A163BF05"+
		"98DA48361C55D39A69163FA8FD24CF5F83655D23DCA3AD961C62F356208552BB"+
		"9ED529077096966D670C354E4ABC9804F1746C08CA18217C32905E462E36CE3B"+
		"E39E772C180E86039B2783A2EC07A28FB5C55DF06F4C52C9DE2BCBF695581718"+
		"3995497CEA956AE515D2261898FA051015728E5A8AAAC42DAD33170D04507A33"+
		"A85521ABDF1CBA64ECFB850458DBEF0A8AEA71575D060C7DB3970F85A6E1E4C7"+
		"ABF5AE8CDB0933D71E8C94E04A25619DCEE3D2261AD2EE6BF12FFA06D98A0864"+
		"D87602733EC86A64521F2B18177B200CBBE117577A615D6C770988C0BAD946E2"+
		"08E24FA074E5AB3143DB5BFCE0FD108E4B82D120A93AD2CAFFFFFFFFFFFFFFFF", 16)
	srpG = big.NewInt(5)
)

func srpHash(parts ...[]byte) []byte {
	h := sha512.New()
	for _, p := range parts {
		h.Write(p)
	}
	return h.Sum(nil)
}

// srpPad left-pads b to the length of N.
func srpPad(b []byte) []byte {
	n := (srpN.BitLen() + 7) / 8
	if len(b) >= n {
		return b
	}
	return append(make([]byte, n-len(b)), b...)
}

type srpClient struct {
	A  []byte // client public key
	M1 []byte // client proof
	K  []byte // session key
	m2 []byte // expected server proof
}

// newSRPClient runs the client side of SRP for the given PIN against the
// server's salt and public key B.
func newSRPClient(pin string, salt, serverPub []byte) (*srpClient, error) {
	return newSRPClientWithKey(pin, salt, serverPub, nil)
}

func newSRPClientWithKey(pin string, salt, serverPub []byte, a *big.Int) (*srpClient, error) {
	B := new(big.Int).SetBytes(serverPub)
	if new(big.Int).Mod(B, srpN).Sign() == 0 {
		return nil, errors.New("airplay: invalid SRP public key")
	}
	if a == nil {
		var err error
		if a, err = rand.Int(rand.Reader, srpN); err != nil {
			return nil, err
		}
	}

	A := new(big.Int).Exp(srpG, a, srpN)
	Abytes := srpPad(A.Bytes())
	k := new(big.Int).SetBytes(srpHash(srpN.Bytes(), srpPad(srpG.Bytes())))
	u := new(big.Int).SetBytes(srpHash(Abytes, srpPad(B.Bytes())))
	x := new(big.Int).SetBytes(srpHash(salt, srpHash([]byte(srpUsername+":"+pin))))

	// S = (B - k*g^x) ^ (a + u*x) mod N
	gx := new(big.Int).Exp(srpG, x, srpN)
	base := new(big.Int).Sub(B, new(big.Int).Mul(k, gx))
	base.Mod(base, srpN)
	exp := new(big.Int).Add(a, new(big.Int).Mul(u, x))
	S := new(big.Int).Exp(base, exp, srpN)
	K := srpHash(S.Bytes())

	hN, hG := srpHash(srpN.Bytes()), srpHash(srpG.Bytes())
	for i := range hN {
		hN[i] ^= hG[i]
	}
	M1 := srpHash(hN, srpHash([]byte(srpUsername)), salt, Abytes, serverPub, K)

	return &srpClient{A: Abytes, M1: M1, K: K, m2: srpHash(Abytes, M1, K)}, nil
}

// verify checks the server proof M2.
func (c *srpClient) verify(m2 []byte) bool {
	return hmac.Equal(c.m2, m2)
}
