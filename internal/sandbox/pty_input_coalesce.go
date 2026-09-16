package sandbox

import (
	"context"
	"errors"
	"time"
)

const (
	ptyInputCoalesceMax = 4096
	ptyInputQueueDepth  = 64
)

var errPtyInputClosed = errors.New("terminal input closed")

// ptyInputCoalescer turns many small Write calls into fewer send() RPCs,
// and never blocks Write on the RPC itself (unless the queue is full).
//
// The first queued chunk is sent immediately. Further keystrokes that arrive
// while that RPC is in flight are drained and sent as one follow-up, so burst
// typing shares RPCs without a Nagle-style pause on the first character.
type ptyInputCoalescer struct {
	send func(context.Context, []byte) error
	ch   chan []byte
	done <-chan struct{}
	ctx  context.Context
}

func newPtyInputCoalescer(
	ctx context.Context,
	done <-chan struct{},
	send func(context.Context, []byte) error,
) *ptyInputCoalescer {
	c := &ptyInputCoalescer{
		send: send,
		ch:   make(chan []byte, ptyInputQueueDepth),
		done: done,
		ctx:  ctx,
	}
	go c.loop()
	return c
}

func (c *ptyInputCoalescer) Write(ctx context.Context, data []byte) error {
	if len(data) == 0 {
		return nil
	}
	chunk := append([]byte(nil), data...)
	select {
	case <-c.done:
		return errPtyInputClosed
	case <-ctx.Done():
		return ctx.Err()
	case c.ch <- chunk:
		return nil
	}
}

func (c *ptyInputCoalescer) loop() {
	for {
		select {
		case <-c.done:
			return
		case first := <-c.ch:
			buf := append([]byte(nil), first...)
			buf = c.drain(buf)
			c.flush(buf)
		}
	}
}

func (c *ptyInputCoalescer) drain(buf []byte) []byte {
	for len(buf) < ptyInputCoalesceMax {
		select {
		case more := <-c.ch:
			buf = append(buf, more...)
		default:
			return buf
		}
	}
	return buf
}

func (c *ptyInputCoalescer) flush(buf []byte) {
	if len(buf) == 0 || c.send == nil {
		return
	}
	select {
	case <-c.done:
		return
	default:
	}
	rctx, cancel := context.WithTimeout(c.ctx, 5*time.Second)
	_ = c.send(rctx, buf)
	cancel()
}
