package packet

import (
	"bytes"
	"compress/zlib"
	"errors"
	"fmt"
	"io"
	"sync"
)

const MaxDataLength = 0x200000

// Packet define a net data package
type Packet struct {
	ID   int32
	Data []byte
}

// Marshal generate Packet with the ID and Fields
func Marshal[ID ~int32 | int](id ID, fields ...FieldEncoder) (pk Packet) {
	var pb Builder
	for _, v := range fields {
		pb.WriteField(v)
	}
	return pb.Packet(int32(id))
}

// Scan decode the packet and fill data into fields
func (p Packet) Scan(fields ...FieldDecoder) error {
	r := bytes.NewReader(p.Data)
	for i, v := range fields {
		_, err := v.ReadFrom(r)
		if err != nil {
			return fmt.Errorf("scanning packet field[%d] error: %w", i, err)
		}
	}
	return nil
}

var (
	bufPool  = sync.Pool{New: func() any { return new(bytes.Buffer) }}
	zlibPool = sync.Pool{New: func() any { return zlib.NewWriter(io.Discard) }}
)

// Pack 打包一个数据包
func (p *Packet) Pack(w io.Writer, threshold int) error {
	if p == nil {
		return errors.New("cannot pack a nil packet")
	}
	if threshold >= 0 {
		return p.packWithCompression(w, threshold)
	} else {
		return p.packWithoutCompression(w)
	}
}

func (p *Packet) packWithoutCompression(w io.Writer) error {
	dataLength, err := p.dataLength()
	if err != nil {
		return err
	}
	buffer := bufPool.Get().(*bytes.Buffer)
	defer bufPool.Put(buffer)
	buffer.Reset()

	// Write Length to buffer
	Length := VarInt(dataLength)
	_, _ = Length.WriteTo(buffer)

	// Write ID and Data to buffer
	_, _ = VarInt(p.ID).WriteTo(buffer)
	buffer.Write(p.Data)

	// Write buffer to w
	return writeFull(w, buffer.Bytes())
}

func (p *Packet) packWithCompression(w io.Writer, threshold int) error {
	dataLength, err := p.dataLength()
	if err != nil {
		return err
	}
	buff := bufPool.Get().(*bytes.Buffer)
	defer bufPool.Put(buff)
	buff.Reset()

	PacketID := VarInt(p.ID)
	if dataLength < threshold {
		DataLength := VarInt(0) // uncompressed mark
		packetLength := DataLength.Len() + dataLength
		if packetLength > MaxDataLength {
			return fmt.Errorf("packet frame length %d exceeds maximum %d", packetLength, MaxDataLength)
		}
		PacketLength := VarInt(packetLength)
		_, _ = PacketLength.WriteTo(buff)
		_, _ = DataLength.WriteTo(buff)
		_, _ = PacketID.WriteTo(buff)
		_, _ = buff.Write(p.Data)
	} else {
		DataLength := VarInt(dataLength)

		buff.Write(make([]byte, MaxVarIntLen)) // padding for Packet Length
		_, _ = DataLength.WriteTo(buff)
		if err := compressPacket(buff, p.ID, p.Data); err != nil {
			return err
		}

		packetLength := buff.Len() - MaxVarIntLen
		if packetLength <= 0 || packetLength > MaxDataLength {
			return fmt.Errorf("compressed packet frame length %d is outside range 1..%d", packetLength, MaxDataLength)
		}
		PacketLength := VarInt(packetLength)
		packetLengthLen := PacketLength.Len()
		buff.Next(MaxVarIntLen - packetLengthLen)
		PacketLength.WriteToBytes(buff.Bytes()[:packetLengthLen])
	}

	return writeFull(w, buff.Bytes())
}

func (p *Packet) dataLength() (int, error) {
	if len(p.Data) > MaxDataLength {
		return 0, fmt.Errorf("packet payload length %d exceeds maximum %d", len(p.Data), MaxDataLength)
	}
	length := VarInt(p.ID).Len() + len(p.Data)
	if length > MaxDataLength {
		return 0, fmt.Errorf("packet data length %d exceeds maximum %d", length, MaxDataLength)
	}
	return length, nil
}

func writeFull(w io.Writer, data []byte) error {
	_, err := io.Copy(w, bytes.NewReader(data))
	return err
}

func compressPacket(w io.Writer, packetID int32, data []byte) error {
	zw := zlibPool.Get().(*zlib.Writer)
	defer zlibPool.Put(zw)
	zw.Reset(w)

	if _, err := VarInt(packetID).WriteTo(zw); err != nil {
		_ = zw.Close()
		return err
	}
	if _, err := zw.Write(data); err != nil {
		_ = zw.Close()
		return err
	}
	return zw.Close()
}

// UnPack in-place decompression a packet
func (p *Packet) UnPack(r io.Reader, threshold int) error {
	if p == nil {
		return errors.New("cannot unpack into a nil packet")
	}
	if threshold >= 0 {
		return p.unpackWithCompression(r, threshold)
	} else {
		return p.unpackWithoutCompression(r)
	}
}

func (p *Packet) unpackWithoutCompression(r io.Reader) error {
	var Length VarInt
	_, err := Length.ReadFrom(r)
	if err != nil {
		return err
	}
	if Length <= 0 || Length > MaxDataLength {
		return fmt.Errorf("uncompressed packet length %d is outside range 1..%d", Length, MaxDataLength)
	}

	lr := &io.LimitedReader{R: r, N: int64(Length)}
	var PacketID VarInt
	_, err = PacketID.ReadFrom(lr)
	if err != nil {
		return fmt.Errorf("read uncompressed packet ID: %w", err)
	}

	lengthOfData := int(lr.N)
	var data []byte
	if cap(p.Data) < lengthOfData {
		data = make([]byte, lengthOfData)
	} else {
		data = p.Data[:lengthOfData]
	}
	_, err = io.ReadFull(lr, data)
	if err != nil {
		return fmt.Errorf("read uncompressed packet data: %w", err)
	}
	p.ID = int32(PacketID)
	p.Data = data
	return nil
}

func (p *Packet) unpackWithCompression(r io.Reader, threshold int) error {
	var PacketLength VarInt
	_, err := PacketLength.ReadFrom(r)
	if err != nil {
		return err
	}
	if PacketLength <= 0 || PacketLength > MaxDataLength {
		return fmt.Errorf("compressed packet length %d is outside range 1..%d", PacketLength, MaxDataLength)
	}

	frame := make([]byte, int(PacketLength))
	if _, err := io.ReadFull(r, frame); err != nil {
		return fmt.Errorf("read compressed packet frame: %w", err)
	}
	frameReader := bytes.NewReader(frame)

	var DataLength VarInt
	_, err = DataLength.ReadFrom(frameReader)
	if err != nil {
		return fmt.Errorf("read compressed packet data length: %w", err)
	}
	if DataLength < 0 {
		return fmt.Errorf("compressed packet has negative data length %d", DataLength)
	}

	if DataLength == 0 {
		return p.unpackCompressionFrameUncompressed(frameReader, threshold)
	}
	if DataLength > MaxDataLength {
		return fmt.Errorf("compressed packet data length %d exceeds maximum %d", DataLength, MaxDataLength)
	}
	if int(DataLength) < threshold {
		return fmt.Errorf("compressed packet data length %d is below threshold %d", DataLength, threshold)
	}

	zr, err := zlib.NewReader(frameReader)
	if err != nil {
		return fmt.Errorf("open compressed packet data: %w", err)
	}
	var decompressed []byte
	if cap(p.Data) < int(DataLength) {
		decompressed = make([]byte, int(DataLength))
	} else {
		decompressed = p.Data[:int(DataLength)]
	}
	if _, err := io.ReadFull(zr, decompressed); err != nil {
		_ = zr.Close()
		return fmt.Errorf("decompressed packet length does not match advertised %d: %w", DataLength, err)
	}
	var extra [1]byte
	n, readErr := zr.Read(extra[:])
	closeErr := zr.Close()
	if n != 0 || readErr == nil {
		return fmt.Errorf("decompressed packet exceeds advertised length %d", DataLength)
	}
	if !errors.Is(readErr, io.EOF) {
		return fmt.Errorf("finish compressed packet data: %w", readErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close compressed packet data: %w", closeErr)
	}
	if frameReader.Len() != 0 {
		return fmt.Errorf("compressed packet has %d trailing bytes", frameReader.Len())
	}
	return p.setDecodedData(decompressed)
}

func (p *Packet) unpackCompressionFrameUncompressed(frame *bytes.Reader, threshold int) error {
	uncompressedLength := frame.Len()
	if uncompressedLength >= threshold {
		return fmt.Errorf("uncompressed packet data length %d is not below compression threshold %d", uncompressedLength, threshold)
	}
	var packetID VarInt
	if _, err := packetID.ReadFrom(frame); err != nil {
		return fmt.Errorf("read uncompressed packet ID: %w", err)
	}
	dataLength := frame.Len()
	var data []byte
	if cap(p.Data) < dataLength {
		data = make([]byte, dataLength)
	} else {
		data = p.Data[:dataLength]
	}
	if _, err := io.ReadFull(frame, data); err != nil {
		return fmt.Errorf("read uncompressed packet data: %w", err)
	}
	p.ID = int32(packetID)
	p.Data = data
	return nil
}

func (p *Packet) setDecodedData(data []byte) error {
	r := bytes.NewReader(data)
	var packetID VarInt
	if _, err := packetID.ReadFrom(r); err != nil {
		return fmt.Errorf("read decompressed packet ID: %w", err)
	}
	dataOffset := len(data) - r.Len()
	payloadLength := len(data) - dataOffset
	copy(data[:payloadLength], data[dataOffset:])
	p.ID = int32(packetID)
	p.Data = data[:payloadLength]
	return nil
}
