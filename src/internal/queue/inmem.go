package queue

import (
	"context"
	"sync"
)

// Inmem is a simple bounded FIFO queue backed by a buffered channel.
// It is safe for concurrent Enqueue/Close.
type Inmem struct {
	mu        sync.Mutex
	ch        chan Task
	closed    bool
	closeOnce sync.Once
}

func NewInmem(buffer int) *Inmem {
	if buffer <= 0 {
		buffer = 256
	}
	return &Inmem{
		ch: make(chan Task, buffer),
	}
}

func (q *Inmem) Enqueue(t Task) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return ErrClosed
	}
	q.ch <- t
	return nil
}

func (q *Inmem) Dequeue(ctx context.Context) (Task, error) {
	select {
	case <-ctx.Done():
		return Task{}, ctx.Err()
	case t, ok := <-q.ch:
		if !ok {
			return Task{}, ErrClosed
		}
		return t, nil
	}
}

func (q *Inmem) Len() int {
	return len(q.ch)
}

func (q *Inmem) Close() {
	q.closeOnce.Do(func() {
		q.mu.Lock()
		q.closed = true
		close(q.ch)
		q.mu.Unlock()
	})
}

