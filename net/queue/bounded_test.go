package queue

import (
	"sync"
	"testing"
	"time"
)

func TestBoundedQueueAppliesBackpressure(t *testing.T) {
	q := NewBoundedQueue(2, 4, func(v string) int { return len(v) })
	if !q.Push("aa") || !q.Push("bb") {
		t.Fatal("initial pushes failed")
	}

	pushed := make(chan bool, 1)
	go func() { pushed <- q.Push("c") }()
	select {
	case <-pushed:
		t.Fatal("Push returned while item limit was full")
	case <-time.After(20 * time.Millisecond):
	}

	if got, ok := q.Pull(); !ok || got != "aa" {
		t.Fatalf("Pull() = %q, %v; want aa, true", got, ok)
	}
	select {
	case ok := <-pushed:
		if !ok {
			t.Fatal("blocked Push failed after capacity became available")
		}
	case <-time.After(time.Second):
		t.Fatal("blocked Push did not resume")
	}
	q.Close()
}

func TestBoundedQueueWeightLimitAndClose(t *testing.T) {
	q := NewBoundedQueue(4, 3, func(v string) int { return len(v) })
	if q.Push("toolarge") {
		t.Fatal("oversized item was accepted")
	}
	if !q.Push("abc") {
		t.Fatal("item at exact weight limit was rejected")
	}

	pushed := make(chan bool, 1)
	go func() { pushed <- q.Push("x") }()
	select {
	case <-pushed:
		t.Fatal("Push returned while weight budget was full")
	case <-time.After(20 * time.Millisecond):
	}

	q.Close()
	select {
	case ok := <-pushed:
		if ok {
			t.Fatal("Push succeeded after Close")
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not unblock Push")
	}
	if got, ok := q.Pull(); !ok || got != "abc" {
		t.Fatalf("Pull after Close = %q, %v; want abc, true", got, ok)
	}
	if _, ok := q.Pull(); ok {
		t.Fatal("drained closed queue remained open")
	}
}

func TestConstructedChannelQueueSerializesPushAndClose(t *testing.T) {
	for range 1000 {
		q := NewChannelQueue[int](1)
		start := make(chan struct{})
		var workers sync.WaitGroup
		workers.Add(2)
		go func() {
			defer workers.Done()
			<-start
			_ = q.Push(1)
		}()
		go func() {
			defer workers.Done()
			<-start
			q.Close()
		}()
		close(start)
		workers.Wait()
		q.Close() // Closing a library-created queue is idempotent.
		for {
			if _, ok := q.Pull(); !ok {
				break
			}
		}
	}
}
