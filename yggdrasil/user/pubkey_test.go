package user

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	pk "github.com/imfusheng/go-mc/net/packet"
)

func TestProfileKeySignaturePayloadFixtures(t *testing.T) {
	const expiresAt = int64(0x0102030405060708)
	encodedKey := []byte{0x00, 0x01, 0x02}
	wantV1 := "72623859790382856-----BEGIN RSA PUBLIC KEY-----\n" +
		"AAEC\n-----END RSA PUBLIC KEY-----\n"
	if got := string(publicKeySignatureV1Payload(expiresAt, encodedKey)); got != wantV1 {
		t.Fatalf("V1 payload = %q, want %q", got, wantV1)
	}

	wrapped := string(publicKeySignatureV1Payload(expiresAt, make([]byte, 58)))
	base64Body := strings.Split(strings.Split(wrapped, "-----BEGIN RSA PUBLIC KEY-----\n")[1], "\n-----END")[0]
	lines := strings.Split(base64Body, "\n")
	if len(lines) != 2 || len(lines[0]) != 76 || len(lines[1]) != 4 {
		t.Fatalf("V1 MIME wrapping line lengths = %v, want [76 4]", []int{len(lines[0]), len(lines[1])})
	}

	profileID := uuid.MustParse("00112233-4455-6677-8899-aabbccddeeff")
	gotV2 := publicKeySignatureV2Payload(profileID, expiresAt, encodedKey)
	wantPrefix, err := hex.DecodeString("00112233445566778899aabbccddeeff0102030405060708")
	if err != nil {
		t.Fatal(err)
	}
	wantV2 := append(wantPrefix, encodedKey...)
	if !bytes.Equal(gotV2, wantV2) {
		t.Fatalf("V2 payload = %x, want %x", gotV2, wantV2)
	}
}

func TestPublicKeyVerifyVersionedCertificates(t *testing.T) {
	servicesKey, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	oldServicesKey := pubKey
	pubKey = &servicesKey.PublicKey
	t.Cleanup(func() { pubKey = oldServicesKey })

	playerKey, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	encodedKey, err := x509.MarshalPKIXPublicKey(&playerKey.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	expiresAt := time.Now().Add(time.Hour).Truncate(time.Millisecond)
	certificate := &PublicKey{ExpiresAt: expiresAt, PubKey: &playerKey.PublicKey}

	certificate.Signature = signServicesPayload(t, servicesKey,
		publicKeySignatureV1Payload(expiresAt.UnixMilli(), encodedKey))
	if !certificate.VerifyV1() || !certificate.Verify() {
		t.Fatal("valid protocol-759 certificate was rejected")
	}
	profileID := uuid.MustParse("00112233-4455-6677-8899-aabbccddeeff")
	if certificate.VerifyV2(profileID) {
		t.Fatal("V1 certificate was accepted as V2")
	}

	certificate.Signature = signServicesPayload(t, servicesKey,
		publicKeySignatureV2Payload(profileID, expiresAt.UnixMilli(), encodedKey))
	if !certificate.VerifyV2(profileID) {
		t.Fatal("valid protocol-760 certificate was rejected")
	}
	if certificate.VerifyV2(uuid.MustParse("10112233-4455-6677-8899-aabbccddeeff")) {
		t.Fatal("V2 certificate was accepted for a different UUID")
	}
	if certificate.VerifyV1() {
		t.Fatal("V2 certificate was accepted as V1")
	}

	certificate.Signature[0] ^= 0x80
	if certificate.VerifyV2(profileID) {
		t.Fatal("tampered V2 signature was accepted")
	}
	certificate.ExpiresAt = time.Now().Add(-time.Second)
	if certificate.VerifyV1() || certificate.VerifyV2(profileID) {
		t.Fatal("expired certificate was accepted")
	}
}

func TestKeyPairRespSelectsCertificateSignatureVersion(t *testing.T) {
	playerKey, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	encodedKey, err := x509.MarshalPKIXPublicKey(&playerKey.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	var keyPair KeyPairResp
	keyPair.KeyPair.PublicKey = string(pem.EncodeToMemory(&pem.Block{Type: "RSA PUBLIC KEY", Bytes: encodedKey}))
	keyPair.PublicKeySignature = base64.StdEncoding.EncodeToString([]byte{0x19})
	keyPair.PublicKeySignatureV2 = base64.StdEncoding.EncodeToString([]byte{0x20})
	keyPair.ExpiresAt = time.UnixMilli(1234)

	for _, test := range []struct {
		name      string
		version   CertificateSignatureVersion
		want      []byte
		useLegacy bool
	}{
		{name: "V1", version: CertificateSignatureV1, want: []byte{0x19}},
		{name: "V2", version: CertificateSignatureV2, want: []byte{0x20}},
		{name: "WriteTo defaults to V2", want: []byte{0x20}, useLegacy: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var encoded bytes.Buffer
			if test.useLegacy {
				_, err = keyPair.WriteTo(&encoded)
			} else {
				_, err = keyPair.Encoder(test.version).WriteTo(&encoded)
			}
			if err != nil {
				t.Fatalf("encode certificate: %v", err)
			}
			var expires pk.Long
			var gotKey, gotSignature pk.ByteArray
			if _, err := (pk.Tuple{&expires, &gotKey, &gotSignature}).ReadFrom(&encoded); err != nil {
				t.Fatalf("decode certificate: %v", err)
			}
			if expires != 1234 || !bytes.Equal(gotKey, encodedKey) || !bytes.Equal(gotSignature, test.want) {
				t.Fatalf("certificate = expiry %d key %x signature %x", expires, gotKey, gotSignature)
			}
		})
	}

	if _, err := keyPair.WriteToVersion(new(bytes.Buffer), CertificateSignatureVersion(99)); err == nil {
		t.Fatal("unsupported certificate signature version was accepted")
	}
}

func signServicesPayload(t *testing.T, key *rsa.PrivateKey, payload []byte) []byte {
	t.Helper()
	digest := sha256.Sum256(payload)
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return signature
}
