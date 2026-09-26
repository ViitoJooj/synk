package engine

import "sync"

// queue is unbounded: push never blocks. That is the point. A bounded queue
// that fills while both sides wait on each other is how two peers deadlock;
// what goes in here is bounded anyway, by the size of a plan.
type queue[T any] struct {
	mu     sync.Mutex
	items  []T
	closed bool
	signal chan struct{}
}

func newQueue[T any]() queue[T] { return queue[T]{signal: make(chan struct{}, 1)} }

func (q *queue[T]) push(v T) {
	q.mu.Lock()
	if !q.closed {
		q.items = append(q.items, v)
	}
	q.mu.Unlock()
	select {
	case q.signal <- struct{}{}:
	default:
	}
}

// pop waits for an item; ok is false once the queue is closed.
func (q *queue[T]) pop() (v T, ok bool) {
	for {
		q.mu.Lock()
		if len(q.items) > 0 {
			v, q.items = q.items[0], q.items[1:]
			q.mu.Unlock()
			return v, true
		}
		if q.closed {
			q.mu.Unlock()
			return v, false
		}
		q.mu.Unlock()
		<-q.signal
	}
}

func (q *queue[T]) close() {
	q.mu.Lock()
	q.closed = true
	q.mu.Unlock()
	select {
	case q.signal <- struct{}{}:
	default:
	}
}
