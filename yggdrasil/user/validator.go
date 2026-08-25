package user

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	_ "embed"
)

//go:embed yggdrasil_session_pubkey.der
var pubKeyBytes []byte
var pubKey = unwrap(x509.ParsePKIXPublicKey(pubKeyBytes)).(*rsa.PublicKey)

// VerifySignature verifies a raw profile-key certificate payload signed by
// Mojang's session-services key. Minecraft 1.19 and 1.19.1+ sign different
// payloads, so callers must construct the version-specific bytes first.
func VerifySignature(payload, signature []byte) bool {
	if pubKey == nil || len(payload) == 0 || len(signature) == 0 {
		return false
	}
	digest := sha256.Sum256(payload)
	return rsa.VerifyPKCS1v15(pubKey, crypto.SHA256, digest[:], signature) == nil
}

func unwrap[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}
