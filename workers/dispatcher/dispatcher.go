package dispatcher

import (
	"context"
	"core/constants"
	"core/types"
	"errors"
	"sync"
)

var ErrNoSubscribers = errors.New("no subscribers for chain")

type Event struct {
	Chain       constants.ChainID
	Type        string
	Transaction *types.TransactionParam
	Ack         chan error
}

type Dispatcher struct {
	subscribers map[constants.ChainID][]chan Event
	mu          sync.RWMutex
	ctx         context.Context
	cancel      context.CancelFunc
	wg          sync.WaitGroup
}

func NewDispatcher() *Dispatcher {
	ctx, cancel := context.WithCancel(context.Background())

	return &Dispatcher{
		subscribers: make(map[constants.ChainID][]chan Event),
		ctx:         ctx,
		cancel:      cancel,
	}
}

func (d *Dispatcher) Subscribe(chain constants.ChainID, buffer int) <-chan Event {
	ch := make(chan Event, buffer)

	d.mu.Lock()
	d.subscribers[chain] = append(d.subscribers[chain], ch)
	d.mu.Unlock()

	return ch
}

func (d *Dispatcher) Unsubscribe(chain constants.ChainID, subChan <-chan Event) {
	d.mu.Lock()
	defer d.mu.Unlock()

	subs := d.subscribers[chain]
	for i, ch := range subs {
		if ch == subChan {
			close(ch)
			d.subscribers[chain] = append(subs[:i], subs[i+1:]...)
			break
		}
	}
}

func (d *Dispatcher) Dispatch(event Event) {
	d.mu.RLock()
	subs := append([]chan Event(nil), d.subscribers[event.Chain]...)
	d.mu.RUnlock()

	for _, ch := range subs {
		select {
		case ch <- event:
		case <-d.ctx.Done():
			return
		}
	}
}

func (d *Dispatcher) DispatchAndWait(ctx context.Context, event Event) error {
	d.mu.RLock()
	subs := append([]chan Event(nil), d.subscribers[event.Chain]...)
	d.mu.RUnlock()

	if len(subs) == 0 {
		return ErrNoSubscribers
	}

	event.Ack = make(chan error, len(subs))
	for _, ch := range subs {
		select {
		case ch <- event:
		case <-ctx.Done():
			return ctx.Err()
		case <-d.ctx.Done():
			return d.ctx.Err()
		}
	}

	var errs []error
	for range subs {
		select {
		case err := <-event.Ack:
			if err != nil {
				errs = append(errs, err)
			}
		case <-ctx.Done():
			return ctx.Err()
		case <-d.ctx.Done():
			return d.ctx.Err()
		}
	}

	return errors.Join(errs...)
}

func (d *Dispatcher) Shutdown() {
	d.cancel()

	d.wg.Wait()
}
