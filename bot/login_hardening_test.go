package bot

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/imfusheng/go-mc/chat"
	"github.com/imfusheng/go-mc/data/packetid"
	pk "github.com/imfusheng/go-mc/net/packet"
	"github.com/imfusheng/go-mc/protocol"
	"github.com/imfusheng/go-mc/yggdrasil/user"
)

func TestScanLoginSuccessConsumesProfileProperties(t *testing.T) {
	profile := protocol.MustByName("1.21.1")
	wantID := uuid.MustParse("12345678-1234-5678-90ab-cdef12345678")
	wantProperties := []user.Property{{Name: "textures", Value: "value", Signature: "signature"}}
	packet := pk.Marshal(
		packetid.ClientboundLoginGameProfile,
		pk.UUID(wantID),
		pk.String("ProfileTest"),
		pk.Array(wantProperties),
		pk.Boolean(false),
	)
	client := NewClient()
	if err := client.scanLoginSuccess(profile, packet); err != nil {
		t.Fatalf("scanLoginSuccess() error = %v", err)
	}
	if client.UUID != wantID || client.Name != "ProfileTest" {
		t.Fatalf("profile = %s/%q", client.UUID, client.Name)
	}
	if len(client.Properties) != 1 || client.Properties[0] != wantProperties[0] {
		t.Fatalf("properties = %#v, want %#v", client.Properties, wantProperties)
	}
}

func TestScanLoginSuccessRejectsMalformedCountsAndTrailingData(t *testing.T) {
	profile := protocol.MustByName("1.21.1")
	wantID := uuid.MustParse("12345678-1234-5678-90ab-cdef12345678")

	negativeCount := pk.Marshal(
		packetid.ClientboundLoginGameProfile,
		pk.UUID(wantID),
		pk.String("ProfileTest"),
		pk.VarInt(-1),
	)
	if err := NewClient().scanLoginSuccess(profile, negativeCount); err == nil {
		t.Fatal("scanLoginSuccess() accepted a negative property count")
	}

	trailing := pk.Marshal(
		packetid.ClientboundLoginGameProfile,
		pk.UUID(wantID),
		pk.String("ProfileTest"),
		pk.VarInt(0),
		pk.Boolean(false),
		pk.Byte(1),
	)
	if err := NewClient().scanLoginSuccess(profile, trailing); err == nil {
		t.Fatal("scanLoginSuccess() accepted trailing data")
	}
}

func TestLoginDisconnectEncodingBoundary(t *testing.T) {
	reason := chat.Text("version boundary")
	for _, test := range []struct {
		version string
		field   pk.FieldEncoder
	}{
		{version: "1.20.2", field: chat.JsonMessage(reason)},
		{version: "1.20.3", field: chat.JsonMessage(reason)},
	} {
		t.Run(test.version, func(t *testing.T) {
			packet := pk.Marshal(packetid.ClientboundLoginLoginDisconnect, test.field)
			got, err := scanLoginDisconnect(protocol.MustByName(test.version), packet)
			if err != nil {
				t.Fatalf("scanLoginDisconnect() error = %v", err)
			}
			if got.ClearString() != reason.ClearString() {
				t.Fatalf("reason = %q, want %q", got.ClearString(), reason.ClearString())
			}
		})
	}
}

func TestScanEncryptionRequestReadsShouldAuthenticate(t *testing.T) {
	profile := protocol.MustByName("1.21.1")
	packet := pk.Marshal(
		packetid.ClientboundLoginHello,
		pk.String(""),
		pk.ByteArray{1, 2, 3},
		pk.ByteArray{4, 5, 6},
		pk.Boolean(false),
	)
	request, err := scanEncryptionRequest(profile, packet)
	if err != nil {
		t.Fatalf("scanEncryptionRequest() error = %v", err)
	}
	if request.ShouldAuthenticate {
		t.Fatal("ShouldAuthenticate = true, want false")
	}

	malformed := pk.Marshal(
		packetid.ClientboundLoginHello,
		pk.String(""),
		pk.VarInt(-1),
	)
	if _, err := scanEncryptionRequest(profile, malformed); err == nil {
		t.Fatal("scanEncryptionRequest() accepted a negative public-key length")
	}
}

func TestEncryptionResponseRejectsNonRSAKeyWithoutPanic(t *testing.T) {
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	_, err = genEncryptionKeyResponse(make([]byte, 16), encoded, []byte{1, 2, 3, 4}, protocol.MustByName("1.21.1"))
	if err == nil || !strings.Contains(err.Error(), "expected RSA") {
		t.Fatalf("genEncryptionKeyResponse() error = %v, want RSA type error", err)
	}
}

func TestLoginStartRejectsInvalidName(t *testing.T) {
	client := NewClient()
	client.Auth.Name = strings.Repeat("a", 17)
	if _, err := client.loginStartPacket(protocol.MustByName("1.21.1"), JoinOptions{}); err == nil {
		t.Fatal("loginStartPacket() accepted a name longer than 16 characters")
	}
}
