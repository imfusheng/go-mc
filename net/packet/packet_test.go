package packet_test

import (
	"bytes"
	"compress/zlib"
	_ "embed"
	"fmt"
	"io"
	"testing"

	pk "github.com/imfusheng/go-mc/net/packet"
)

//go:embed joingame_test.bin
var testJoinGameData []byte

func ExamplePacket_Scan_joinGame() {
	p := pk.Packet{ID: 0x24, Data: testJoinGameData}
	var (
		EID            pk.Int
		Hardcore       pk.Boolean
		Gamemode       pk.UnsignedByte
		PreGamemode    pk.Byte
		WorldNames     = []pk.Identifier{} // This cannot replace with "var DimensionNames []pk.Identifier" because "nil" has no type information
		DimensionCodec struct {
			DimensionType any `nbt:"minecraft:dimension_type"`
			WorldgenBiome any `nbt:"minecraft:worldgen/biome"`
		}
		Dimension                 any
		WorldName                 pk.Identifier
		HashedSeed                pk.Long
		MaxPlayers                pk.VarInt
		ViewDistance              pk.VarInt
		RDI, ERS, IsDebug, IsFlat pk.Boolean
	)
	err := p.Scan(
		&EID,
		&Hardcore,
		&Gamemode,
		&PreGamemode,
		pk.Array(&WorldNames),
		pk.NBT(&DimensionCodec),
		pk.NBT(&Dimension),
		&WorldName,
		&HashedSeed,
		&MaxPlayers,
		&ViewDistance,
		&RDI, &ERS, &IsDebug, &IsFlat,
	)
	fmt.Print(err)
	// Output: <nil>
}

func ExampleMarshal_setSlot() {
	for _, pf := range []struct {
		WindowID  byte
		Slot      int16
		Present   bool
		ItemID    int
		ItemCount byte
		NBT       any
	}{
		{WindowID: 0, Slot: 5, Present: false},
		{WindowID: 0, Slot: 5, Present: true, ItemID: 0x01, ItemCount: 1, NBT: pk.Byte(0)},
		{WindowID: 0, Slot: 5, Present: true, ItemID: 0x01, ItemCount: 1, NBT: pk.NBT(int32(0x12345678))},
	} {
		p := pk.Marshal(0x15,
			pk.Byte(pf.WindowID),
			pk.Short(pf.Slot),
			pk.Boolean(pf.Present),
			pk.Opt{Has: pf.Present, Field: pk.Tuple{
				pk.VarInt(pf.ItemID),
				pk.Byte(pf.ItemCount),
				pf.NBT,
			}},
		)
		fmt.Printf("%02X % 02X\n", p.ID, p.Data)
	}
	// Output:
	// 15 00 00 05 00
	// 15 00 00 05 01 01 01 00
	// 15 00 00 05 01 01 01 03 12 34 56 78
}

func BenchmarkPacket_Pack_packWithoutCompression(b *testing.B) {
	p := pk.Packet{ID: 0, Data: make([]byte, 64)}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := p.Pack(io.Discard, -1); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPacket_Pack_packWithCompression(b *testing.B) {
	p := pk.Packet{ID: 0, Data: make([]byte, 64)}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := p.Pack(io.Discard, 32); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPacket_UnPack_withoutCompression(b *testing.B) {
	want := pk.Packet{ID: 300, Data: bytes.Repeat([]byte{0xab}, 64)}
	var wire bytes.Buffer
	if err := want.Pack(&wire, -1); err != nil {
		b.Fatal(err)
	}
	encoded := append([]byte(nil), wire.Bytes()...)
	var got pk.Packet
	b.SetBytes(int64(len(encoded)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := got.UnPack(bytes.NewReader(encoded), -1); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPacket_UnPack_withCompression(b *testing.B) {
	want := pk.Packet{ID: 300, Data: bytes.Repeat([]byte{0xab}, 64)}
	var wire bytes.Buffer
	if err := want.Pack(&wire, 32); err != nil {
		b.Fatal(err)
	}
	encoded := append([]byte(nil), wire.Bytes()...)
	var got pk.Packet
	b.SetBytes(int64(len(encoded)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := got.UnPack(bytes.NewReader(encoded), 32); err != nil {
			b.Fatal(err)
		}
	}
}

func encodePacketTestVarInt(t *testing.T, value pk.VarInt) []byte {
	t.Helper()
	var out bytes.Buffer
	if _, err := value.WriteTo(&out); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func packetTestFrame(t *testing.T, body []byte) []byte {
	t.Helper()
	frame := append([]byte(nil), encodePacketTestVarInt(t, pk.VarInt(len(body)))...)
	return append(frame, body...)
}

func packetTestZlib(t *testing.T, data []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	zw := zlib.NewWriter(&out)
	if _, err := zw.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func packetTestCompressedFrame(t *testing.T, advertisedLength pk.VarInt, decompressed []byte) []byte {
	t.Helper()
	body := append([]byte(nil), encodePacketTestVarInt(t, advertisedLength)...)
	body = append(body, packetTestZlib(t, decompressed)...)
	return packetTestFrame(t, body)
}

func TestPacketRoundTripCompressionModes(t *testing.T) {
	want := pk.Packet{ID: 300, Data: bytes.Repeat([]byte{0xab}, 64)}
	for _, threshold := range []int{-1, 0, 128} {
		t.Run(fmt.Sprintf("threshold_%d", threshold), func(t *testing.T) {
			var wire bytes.Buffer
			if err := want.Pack(&wire, threshold); err != nil {
				t.Fatal(err)
			}
			var got pk.Packet
			if err := got.UnPack(&wire, threshold); err != nil {
				t.Fatal(err)
			}
			if got.ID != want.ID || !bytes.Equal(got.Data, want.Data) {
				t.Fatalf("round trip = {ID:%d Data:%x}, want {ID:%d Data:%x}", got.ID, got.Data, want.ID, want.Data)
			}
		})
	}
}

func TestPacketCompressionThresholdIncludesPacketID(t *testing.T) {
	packet := pk.Packet{ID: 0}
	var wire bytes.Buffer
	if err := packet.Pack(&wire, 1); err != nil {
		t.Fatal(err)
	}
	var packetLength, dataLength pk.VarInt
	if _, err := packetLength.ReadFrom(&wire); err != nil {
		t.Fatal(err)
	}
	if _, err := dataLength.ReadFrom(&wire); err != nil {
		t.Fatal(err)
	}
	if dataLength != 1 {
		t.Fatalf("data length = %d, want 1 (compressed)", dataLength)
	}
}

func TestUnpackWithoutCompressionRejectsInvalidFrames(t *testing.T) {
	tests := []struct {
		name string
		wire []byte
	}{
		{name: "negative length", wire: encodePacketTestVarInt(t, -1)},
		{name: "zero length", wire: []byte{0}},
		{name: "oversized length", wire: encodePacketTestVarInt(t, pk.MaxDataLength+1)},
		{name: "truncated frame", wire: append(encodePacketTestVarInt(t, 3), 0, 1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if recovered := recover(); recovered != nil {
					t.Fatalf("UnPack panicked: %v", recovered)
				}
			}()
			var packet pk.Packet
			if err := packet.UnPack(bytes.NewReader(tt.wire), -1); err == nil {
				t.Fatal("UnPack succeeded, want error")
			}
		})
	}
}

func TestUnpackWithoutCompressionBoundsPacketIDToFrame(t *testing.T) {
	wire := bytes.NewReader([]byte{1, 0x80, 0x2a})
	var packet pk.Packet
	if err := packet.UnPack(wire, -1); err == nil {
		t.Fatal("UnPack succeeded, want truncated packet ID error")
	}
	if wire.Len() != 1 {
		t.Fatalf("UnPack consumed %d bytes beyond its frame", 1-wire.Len())
	}
	last, _ := wire.ReadByte()
	if last != 0x2a {
		t.Fatalf("byte after frame = %#x, want 0x2a", last)
	}
}

func TestUnpackWithCompressionRejectsInvalidLengthsAndThresholds(t *testing.T) {
	compressedID := packetTestZlib(t, []byte{0})
	tests := []struct {
		name      string
		wire      []byte
		threshold int
	}{
		{name: "negative packet length", wire: encodePacketTestVarInt(t, -1), threshold: 0},
		{name: "zero packet length", wire: []byte{0}, threshold: 0},
		{name: "oversized packet length", wire: encodePacketTestVarInt(t, pk.MaxDataLength+1), threshold: 0},
		{name: "negative data length", wire: packetTestFrame(t, encodePacketTestVarInt(t, -1)), threshold: 0},
		{name: "oversized data length", wire: packetTestFrame(t, append(encodePacketTestVarInt(t, pk.MaxDataLength+1), compressedID...)), threshold: 0},
		{name: "compressed below threshold", wire: packetTestCompressedFrame(t, 1, []byte{0}), threshold: 2},
		{name: "uncompressed at threshold", wire: packetTestFrame(t, []byte{0, 0}), threshold: 1},
		{name: "truncated outer frame", wire: append(encodePacketTestVarInt(t, 3), 0, 0), threshold: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if recovered := recover(); recovered != nil {
					t.Fatalf("UnPack panicked: %v", recovered)
				}
			}()
			var packet pk.Packet
			if err := packet.UnPack(bytes.NewReader(tt.wire), tt.threshold); err == nil {
				t.Fatal("UnPack succeeded, want error")
			}
		})
	}
}

func TestUnpackWithCompressionRejectsMalformedStreams(t *testing.T) {
	validPayload := []byte{0, 1, 2, 3}
	truncatedBody := append(encodePacketTestVarInt(t, pk.VarInt(len(validPayload))), packetTestZlib(t, validPayload)...)
	truncatedBody = truncatedBody[:len(truncatedBody)-2]

	trailingBody := append(encodePacketTestVarInt(t, pk.VarInt(len(validPayload))), packetTestZlib(t, validPayload)...)
	trailingBody = append(trailingBody, 0xff)

	corruptBody := append(encodePacketTestVarInt(t, pk.VarInt(len(validPayload))), packetTestZlib(t, validPayload)...)
	corruptBody[len(corruptBody)-1] ^= 0xff

	tests := []struct {
		name string
		wire []byte
	}{
		{name: "advertised longer", wire: packetTestCompressedFrame(t, 5, validPayload)},
		{name: "advertised shorter", wire: packetTestCompressedFrame(t, 3, validPayload)},
		{name: "bounded decompression bomb", wire: packetTestCompressedFrame(t, 1, bytes.Repeat([]byte{0}, 1<<20))},
		{name: "truncated zlib stream", wire: packetTestFrame(t, truncatedBody)},
		{name: "trailing compressed bytes", wire: packetTestFrame(t, trailingBody)},
		{name: "invalid zlib checksum", wire: packetTestFrame(t, corruptBody)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var packet pk.Packet
			if err := packet.UnPack(bytes.NewReader(tt.wire), 0); err == nil {
				t.Fatal("UnPack succeeded, want error")
			}
		})
	}
}

func TestPackRejectsOversizedDataAndFrame(t *testing.T) {
	tooLarge := pk.Packet{ID: 0, Data: make([]byte, pk.MaxDataLength)}
	for _, threshold := range []int{-1, 0} {
		if err := tooLarge.Pack(io.Discard, threshold); err == nil {
			t.Fatalf("Pack(threshold=%d) accepted oversized packet data", threshold)
		}
	}

	maxData := pk.Packet{ID: 0, Data: make([]byte, pk.MaxDataLength-1)}
	if err := maxData.Pack(io.Discard, pk.MaxDataLength+1); err == nil {
		t.Fatal("Pack accepted a compression frame made oversized by its data-length marker")
	}
}

type packetTestShortWriter struct{}

func (packetTestShortWriter) Write([]byte) (int, error) { return 0, nil }

func TestPackRejectsShortWrites(t *testing.T) {
	packet := pk.Packet{ID: 0, Data: []byte{1}}
	if err := packet.Pack(packetTestShortWriter{}, -1); err == nil {
		t.Fatal("Pack succeeded after a short write")
	}
}

func FuzzPacketUnpackDoesNotPanic(f *testing.F) {
	f.Add([]byte{1, 0}, false)
	f.Add([]byte{2, 0, 0}, true)
	f.Add([]byte{0xff, 0xff, 0xff, 0xff, 0x0f}, true)
	f.Fuzz(func(t *testing.T, data []byte, compression bool) {
		if len(data) > 4096 {
			t.Skip()
		}
		threshold := -1
		if compression {
			threshold = 0
		}
		var packet pk.Packet
		_ = packet.UnPack(bytes.NewReader(data), threshold)
	})
}
