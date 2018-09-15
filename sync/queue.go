package astisync

import (
	"context"
	"sync"
	"sync/atomic"
)

// CtxQueue is a queue that can
// - handle a context without dropping any messages sent previously
// - ensure that sending a message is not blocking even if the context has been cancelled and the queue has not been started
type CtxQueue struct {
	c          chan ctxQueueMessage
	ctxIsDone  uint32
	hasStarted uint32
	o          *sync.Once
	startC     *sync.Cond
}

type ctxQueueMessage struct {
	c         *sync.Cond
	ctxIsDone bool
	p         interface{}
}

// NewCtxQueue creates a new ctx queue
func NewCtxQueue() *CtxQueue {
	return &CtxQueue{
		c:      make(chan ctxQueueMessage),
		o:      &sync.Once{},
		startC: sync.NewCond(&sync.Mutex{}),
	}
}

// HandleCtx handles the ctx
func (q *CtxQueue) HandleCtx(ctx context.Context) {
	// Wait for ctx to be done
	<-ctx.Done()

	// Broadcast
	q.startC.L.Lock()
	atomic.StoreUint32(&q.ctxIsDone, 1)
	q.startC.Broadcast()
	q.startC.L.Unlock()

	// If the queue has started, send the ctx message
	if d := atomic.LoadUint32(&q.hasStarted); d == 1 {
		q.c <- ctxQueueMessage{ctxIsDone: true}
	}
}

// Start starts the queue
func (q *CtxQueue) Start(fn func(p interface{})) {
	// Make sure the queue can only be started once
	q.o.Do(func() {
		// Broadcast
		q.startC.L.Lock()
		q.startC.Broadcast()
		atomic.StoreUint32(&q.hasStarted, 1)
		q.startC.L.Unlock()

		// Loop
		for {
			select {
			case m := <-q.c:
				// Check context
				if m.ctxIsDone {
					return
				}

				// Handle payload
				fn(m.p)

				// Broadcast the fact that the process is done
				if m.c != nil {
					m.c.L.Lock()
					m.c.Broadcast()
					m.c.L.Unlock()
				}
			}
		}
	})
}

// Send sends a message in the queue
// Block indicates whether to block until the message has been fully processed
func (q *CtxQueue) Send(p interface{}, block bool) {
	// Make sure to lock here
	q.startC.L.Lock()

	// Context is done
	if d := atomic.LoadUint32(&q.ctxIsDone); d == 1 {
		return
	}

	// Check whether queue has been started
	if d := atomic.LoadUint32(&q.hasStarted); d == 0 {
		// We either wait for the queue to start or for the ctx to be done
		q.startC.Wait()

		// Context is done
		if d := atomic.LoadUint32(&q.ctxIsDone); d == 1 {
			q.startC.L.Unlock()
			return
		}
	}
	q.startC.L.Unlock()

	// Create cond
	var c *sync.Cond
	if block {
		c = sync.NewCond(&sync.Mutex{})
		c.L.Lock()
	}

	// Send message
	q.c <- ctxQueueMessage{
		c: c,
		p: p,
	}

	// Wait for handling to be done
	if block {
		c.Wait()
	}
}