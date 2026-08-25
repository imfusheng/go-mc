package queue

import (
	"testing"

	pk "github.com/imfusheng/go-mc/net/packet"
)

func BenchmarkLinkedListQueueRoundTrip(b *testing.B) {
	q := NewLinkedQueue[int]()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if !q.Push(i) {
			b.Fatal("push failed")
		}
		if _, ok := q.Pull(); !ok {
			b.Fatal("pull failed")
		}
	}
}

func BenchmarkChannelQueueRoundTrip(b *testing.B) {
	q := NewChannelQueue[int](1)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if !q.Push(i) {
			b.Fatal("push failed")
		}
		if _, ok := q.Pull(); !ok {
			b.Fatal("pull failed")
		}
	}
}

func BenchmarkLinkedListPacketQueueRoundTrip(b *testing.B) {
	q := NewLinkedQueue[pk.Packet]()
	packet := pk.Packet{ID: 1, Data: make([]byte, 64)}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if !q.Push(packet) {
			b.Fatal("push failed")
		}
		if _, ok := q.Pull(); !ok {
			b.Fatal("pull failed")
		}
	}
}

func BenchmarkChannelPacketQueueRoundTrip(b *testing.B) {
	q := NewChannelQueue[pk.Packet](1)
	packet := pk.Packet{ID: 1, Data: make([]byte, 64)}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if !q.Push(packet) {
			b.Fatal("push failed")
		}
		if _, ok := q.Pull(); !ok {
			b.Fatal("pull failed")
		}
	}
}
