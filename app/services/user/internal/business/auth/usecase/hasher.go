package usecase

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
)

type Hasher struct {
	hash []byte
}

func NewHasher(h []byte) (Hasher, error) {
	return Hasher{
		hash: h,
	}, nil
}

func (h Hasher) Hash(phone, code string) string {
	return hex.EncodeToString(mac(h.hash, phone, code))
}

func (h Hasher) Verify(stored, phoneE164, code string) bool {
	want, err := hex.DecodeString(stored)
	if err != nil {
		return false
	}
	if subtle.ConstantTimeCompare(want, mac(h.hash, phoneE164, code)) == 1 {
		return true
	}
	return false
}

func mac(pepper []byte, phoneE164, code string) []byte {
	m := hmac.New(sha256.New, pepper)
	m.Write([]byte(phoneE164))
	m.Write([]byte{0}) // separator: prevents ("+2438", "12...") colliding with ("+243", "812...")
	m.Write([]byte(code))
	return m.Sum(nil)
}
