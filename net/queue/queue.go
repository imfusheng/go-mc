package queue

import (
	"container/list"
	"sync"
)

type Queue[T any] interface {
	Push(v T) (ok bool)
	Pull() (v T, ok bool)
	Close()
}

// NewBoundedQueue creates a blocking ring queue with both item and cumulative
// weight limits. Push applies backpressure while the queue is full and returns
// false when the queue is closed or one item exceeds maxWeight. weight must be
// deterministic and return a non-negative value.
func NewBoundedQueue[T any](maxItems, maxWeight int, weight func(T) int) Queue[T] {
	if maxItems <= 0 {
		panic("queue: maxItems must be positive")
	}
	if maxWeight <= 0 {
		panic("queue: maxWeight must be positive")
	}
	if weight == nil {
		panic("queue: weight function must not be nil")
	}

	q := &BoundedQueue[T]{
		buffer:    make([]weightedItem[T], maxItems),
		maxWeight: maxWeight,
		weightOf:  weight,
	}
	q.notEmpty = sync.NewCond(&q.mu)
	q.notFull = sync.NewCond(&q.mu)
	return q
}

type weightedItem[T any] struct {
	value  T
	weight int
}

// BoundedQueue is the ring implementation returned by NewBoundedQueue.
type BoundedQueue[T any] struct {
	mu        sync.Mutex
	notEmpty  *sync.Cond
	notFull   *sync.Cond
	buffer    []weightedItem[T]
	head      int
	size      int
	weight    int
	maxWeight int
	weightOf  func(T) int
	closed    bool
}

func (q *BoundedQueue[T]) Push(v T) bool {
	itemWeight := q.weightOf(v)
	if itemWeight < 0 || itemWeight > q.maxWeight {
		return false
	}

	q.mu.Lock()
	defer q.mu.Unlock()
	for !q.closed && (q.size == len(q.buffer) || q.weight > q.maxWeight-itemWeight) {
		q.notFull.Wait()
	}
	if q.closed {
		return false
	}

	index := (q.head + q.size) % len(q.buffer)
	q.buffer[index] = weightedItem[T]{value: v, weight: itemWeight}
	q.size++
	q.weight += itemWeight
	q.notEmpty.Signal()
	return true
}

func (q *BoundedQueue[T]) Pull() (v T, ok bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for q.size == 0 && !q.closed {
		q.notEmpty.Wait()
	}
	if q.size == 0 {
		return v, false
	}

	item := q.buffer[q.head]
	var zero weightedItem[T]
	q.buffer[q.head] = zero
	q.head = (q.head + 1) % len(q.buffer)
	q.size--
	q.weight -= item.weight
	q.notFull.Broadcast()
	return item.value, true
}

func (q *BoundedQueue[T]) Close() {
	q.mu.Lock()
	if !q.closed {
		q.closed = true
		q.notEmpty.Broadcast()
		q.notFull.Broadcast()
	}
	q.mu.Unlock()
}

func NewLinkedQueue[T any]() (q Queue[T]) {
	return &LinkedListQueue[T]{
		queue: list.New(),
		cond:  sync.Cond{L: new(sync.Mutex)},
	}
}

type LinkedListQueue[T any] struct {
	queue  *list.List
	closed bool
	cond   sync.Cond
}

func (p *LinkedListQueue[T]) Push(v T) bool {
	p.cond.L.Lock()
	if p.closed {
		panic("push on closed queue")
	}
	p.queue.PushBack(v)
	p.cond.Signal()
	p.cond.L.Unlock()
	return true
}

func (p *LinkedListQueue[T]) Pull() (v T, ok bool) {
	p.cond.L.Lock()
	for {
		if elem := p.queue.Front(); elem != nil {
			v = p.queue.Remove(elem).(T)
			ok = true
			break
		} else if p.closed {
			break
		}
		p.cond.Wait()
	}
	p.cond.L.Unlock()
	return
}

func (p *LinkedListQueue[T]) Close() {
	p.cond.L.Lock()
	p.closed = true
	p.cond.Broadcast()
	p.cond.L.Unlock()
}

func NewChannelQueue[T any](n int) (q Queue[T]) {
	if n < 0 {
		panic("queue: channel capacity must not be negative")
	}
	return &safeChannelQueue[T]{channel: make(chan T, n)}
}

// ChannelQueue is the historical raw-channel implementation.
//
// Deprecated: use NewChannelQueue, whose returned queue serializes Push and
// Close. A directly constructed ChannelQueue cannot make channel send and
// close race-free.
type ChannelQueue[T any] chan T

func (c ChannelQueue[T]) Push(v T) bool {
	select {
	case c <- v:
		return true
	default:
		return false
	}
}

func (c ChannelQueue[T]) Pull() (v T, ok bool) {
	v, ok = <-c
	return
}

func (c ChannelQueue[T]) Close() {
	close(c)
}

// safeChannelQueue keeps the allocation-free, non-blocking channel behavior
// while making the constructor's queue safe when a connection closes at the
// same time another goroutine calls Push. Pull needs no lock: receive and close
// are concurrency-safe channel operations.
type safeChannelQueue[T any] struct {
	mu      sync.RWMutex
	channel chan T
	closed  bool
}

func (q *safeChannelQueue[T]) Push(v T) bool {
	q.mu.RLock()
	defer q.mu.RUnlock()
	if q.closed {
		return false
	}
	select {
	case q.channel <- v:
		return true
	default:
		return false
	}
}

func (q *safeChannelQueue[T]) Pull() (v T, ok bool) {
	v, ok = <-q.channel
	return
}

func (q *safeChannelQueue[T]) Close() {
	q.mu.Lock()
	if !q.closed {
		q.closed = true
		close(q.channel)
	}
	q.mu.Unlock()
}
