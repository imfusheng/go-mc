package user

import (
	"crypto"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	pk "github.com/imfusheng/go-mc/net/packet"
)

type PublicKey struct {
	ExpiresAt time.Time
	PubKey    *rsa.PublicKey
	Signature []byte
}

func (p PublicKey) WriteTo(w io.Writer) (n int64, err error) {
	pubKeyEncoded, err := x509.MarshalPKIXPublicKey(p.PubKey)
	if err != nil {
		return 0, err
	}
	return pk.Tuple{
		pk.Long(p.ExpiresAt.UnixMilli()),
		pk.ByteArray(pubKeyEncoded),
		pk.ByteArray(p.Signature),
	}.WriteTo(w)
}

func (p *PublicKey) ReadFrom(r io.Reader) (n int64, err error) {
	var (
		ExpiresAt pk.Long
		PubKey    pk.ByteArray
		Signature pk.ByteArray
	)
	n, err = pk.Tuple{
		&ExpiresAt,
		&PubKey,
		&Signature,
	}.ReadFrom(r)
	if err != nil {
		return n, err
	}
	p.ExpiresAt = time.UnixMilli(int64(ExpiresAt))
	pubKey, err := x509.ParsePKIXPublicKey(PubKey)
	if err != nil {
		return n, err
	}
	if key, ok := pubKey.(*rsa.PublicKey); !ok {
		return n, errors.New("expect RSA public key")
	} else {
		p.PubKey = key
	}

	p.Signature = Signature
	return n, nil
}

func (p *PublicKey) Verify() bool {
	return p.VerifyV1()
}

// VerifyV1 validates the certificate format used by Minecraft 1.19
// (protocol 759). Its signed payload is the decimal expiry timestamp followed
// immediately by Mojang's 76-column RSA PUBLIC KEY PEM representation.
func (p *PublicKey) VerifyV1() bool {
	encoded, ok := p.encodedUnexpiredKey()
	if !ok {
		return false
	}
	return VerifySignature(publicKeySignatureV1Payload(p.ExpiresAt.UnixMilli(), encoded), p.Signature)
}

// VerifyV2 validates the certificate format used by Minecraft 1.19.1 and
// 1.19.2 (protocol 760). The signature binds the key to the player's UUID.
func (p *PublicKey) VerifyV2(profileID uuid.UUID) bool {
	if profileID == uuid.Nil {
		return false
	}
	encoded, ok := p.encodedUnexpiredKey()
	if !ok {
		return false
	}
	return VerifySignature(publicKeySignatureV2Payload(profileID, p.ExpiresAt.UnixMilli(), encoded), p.Signature)
}

func (p *PublicKey) VerifyMessage(hash, signature []byte) error {
	if p == nil || p.PubKey == nil {
		return errors.New("missing RSA public key")
	}
	return rsa.VerifyPKCS1v15(p.PubKey, crypto.SHA256, hash, signature)
}

func (p *PublicKey) encodedUnexpiredKey() ([]byte, bool) {
	if p == nil || p.PubKey == nil || p.ExpiresAt.Before(time.Now()) {
		return nil, false
	}
	encoded, err := x509.MarshalPKIXPublicKey(p.PubKey)
	return encoded, err == nil
}

func publicKeySignatureV1Payload(expiresAtMillis int64, encodedKey []byte) []byte {
	encoded := base64.StdEncoding.EncodeToString(encodedKey)
	var payload strings.Builder
	payload.Grow(20 + len(encoded) + len(encoded)/76 + 64)
	payload.WriteString(strconv.FormatInt(expiresAtMillis, 10))
	payload.WriteString("-----BEGIN RSA PUBLIC KEY-----\n")
	for start := 0; start < len(encoded); start += 76 {
		if start > 0 {
			payload.WriteByte('\n')
		}
		end := min(start+76, len(encoded))
		payload.WriteString(encoded[start:end])
	}
	payload.WriteString("\n-----END RSA PUBLIC KEY-----\n")
	return []byte(payload.String())
}

func publicKeySignatureV2Payload(profileID uuid.UUID, expiresAtMillis int64, encodedKey []byte) []byte {
	payload := make([]byte, 24+len(encodedKey))
	copy(payload[:16], profileID[:])
	binary.BigEndian.PutUint64(payload[16:24], uint64(expiresAtMillis))
	copy(payload[24:], encodedKey)
	return payload
}
