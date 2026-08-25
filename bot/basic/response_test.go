package basic

import (
	"bytes"
	"testing"

	"github.com/imfusheng/go-mc/data/packetid"
	pk "github.com/imfusheng/go-mc/net/packet"
)

func TestKeepAliveResponseOwnsItsData(t *testing.T) {
	want := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	incoming := pk.Packet{Data: append([]byte(nil), want...)}
	var id pk.Long
	if err := incoming.Scan(&id); err != nil {
		t.Fatal(err)
	}
	response := keepAliveResponse(id)
	if response.ID != int32(packetid.ServerboundKeepAlive) || !bytes.Equal(response.Data, want) {
		t.Fatalf("keepAliveResponse() = {ID:%d Data:%x}, want {ID:%d Data:%x}", response.ID, response.Data, packetid.ServerboundKeepAlive, want)
	}

	copy(incoming.Data, bytes.Repeat([]byte{0xff}, len(incoming.Data)))
	if !bytes.Equal(response.Data, want) {
		t.Fatalf("response aliases the borrowed receive buffer: %x", response.Data)
	}
}

func TestPingResponseOwnsItsData(t *testing.T) {
	want := []byte{1, 2, 3, 4}
	incoming := pk.Packet{Data: append([]byte(nil), want...)}
	var id pk.Int
	if err := incoming.Scan(&id); err != nil {
		t.Fatal(err)
	}
	response := pingResponse(id)
	if response.ID != int32(packetid.ServerboundPong) || !bytes.Equal(response.Data, want) {
		t.Fatalf("pingResponse() = {ID:%d Data:%x}, want {ID:%d Data:%x}", response.ID, response.Data, packetid.ServerboundPong, want)
	}
	copy(incoming.Data, bytes.Repeat([]byte{0xff}, len(incoming.Data)))
	if !bytes.Equal(response.Data, want) {
		t.Fatalf("response aliases the borrowed receive buffer: %x", response.Data)
	}
}
