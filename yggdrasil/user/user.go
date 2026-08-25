package user

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	pk "github.com/imfusheng/go-mc/net/packet"
)

var ServicesURL = "https://api.minecraftservices.com"

var client = &http.Client{Timeout: 30 * time.Second}

type KeyPairResp struct {
	KeyPair struct {
		PrivateKey string `json:"privateKey"`
		PublicKey  string `json:"publicKey"`
	} `json:"keyPair"`
	PublicKeySignature   string    `json:"publicKeySignature"`
	PublicKeySignatureV2 string    `json:"publicKeySignatureV2"`
	ExpiresAt            time.Time `json:"expiresAt"`
	RefreshedAfter       time.Time `json:"refreshedAfter"`
}

// CertificateSignatureVersion selects the Mojang certificate signature sent
// in Login Start. Protocol 759 uses V1; protocol 760 uses V2.
type CertificateSignatureVersion uint8

const (
	CertificateSignatureV1 CertificateSignatureVersion = iota + 1
	CertificateSignatureV2
)

func (k KeyPairResp) WriteTo(w io.Writer) (int64, error) {
	return k.WriteToVersion(w, CertificateSignatureV2)
}

// Encoder returns a packet field encoder for a specific certificate signature
// generation. It lets protocol-aware callers retain packet.FieldEncoder APIs.
func (k KeyPairResp) Encoder(version CertificateSignatureVersion) pk.FieldEncoder {
	return keyPairEncoder{keyPair: k, version: version}
}

// WriteToVersion writes the public-key certificate fields used by Login Start.
func (k KeyPairResp) WriteToVersion(w io.Writer, version CertificateSignatureVersion) (int64, error) {
	block, _ := pem.Decode([]byte(k.KeyPair.PublicKey))
	if block == nil {
		return 0, errors.New("pem decode error: no data is found")
	}
	var signatureText string
	switch version {
	case CertificateSignatureV1:
		signatureText = k.PublicKeySignature
	case CertificateSignatureV2:
		signatureText = k.PublicKeySignatureV2
	default:
		return 0, fmt.Errorf("unsupported certificate signature version %d", version)
	}
	signature, err := base64.StdEncoding.DecodeString(signatureText)
	if err != nil {
		return 0, err
	}
	return pk.Tuple{
		pk.Long(k.ExpiresAt.UnixMilli()),
		pk.ByteArray(block.Bytes),
		pk.ByteArray(signature),
	}.WriteTo(w)
}

type keyPairEncoder struct {
	keyPair KeyPairResp
	version CertificateSignatureVersion
}

func (e keyPairEncoder) WriteTo(w io.Writer) (int64, error) {
	return e.keyPair.WriteToVersion(w, e.version)
}

func GetOrFetchKeyPair(accessToken string) (KeyPairResp, error) {
	return GetOrFetchKeyPairContext(context.Background(), accessToken)
}

// GetOrFetchKeyPairContext fetches the profile key pair while honoring ctx.
func GetOrFetchKeyPairContext(ctx context.Context, accessToken string) (KeyPairResp, error) {
	return fetchKeyPair(ctx, accessToken) // TODO: cache
}

func fetchKeyPair(ctx context.Context, accessToken string) (KeyPairResp, error) {
	var keyPairResp KeyPairResp
	err := postContext(ctx, "/player/certificates", accessToken, &keyPairResp)
	return keyPairResp, err
}

func post(endpoint string, accessToken string, resp any) error {
	return postContext(context.Background(), endpoint, accessToken, resp)
}

func postContext(ctx context.Context, endpoint string, accessToken string, resp any) error {
	rowResp, err := rawPostContext(ctx, endpoint, accessToken)
	if err != nil {
		return fmt.Errorf("request fail: %w", err)
	}
	defer rowResp.Body.Close()
	err = json.NewDecoder(rowResp.Body).Decode(resp)
	if err != nil {
		return fmt.Errorf("parse resp fail: %v", err)
	}

	return nil
}

func rawPost(endpoint string, accessToken string) (*http.Response, error) {
	return rawPostContext(context.Background(), endpoint, accessToken)
}

func rawPostContext(ctx context.Context, endpoint string, accessToken string) (*http.Response, error) {
	postRequest, err := http.NewRequestWithContext(
		ctx, http.MethodPost, ServicesURL+endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("make request error: %v", err)
	}

	postRequest.Header.Set("Authorization", "Bearer "+accessToken)
	postRequest.Header.Set("Content-Type", "application/json; charset=utf-8")

	// Do
	return client.Do(postRequest)
}
