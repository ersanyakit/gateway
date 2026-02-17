package dispatcher

import (
	"context"
	"core/constants"
	"core/types"
	"sync"
)

type Event struct {
	Chain       constants.ChainID
	Type        string
	Transaction *types.TransactionParam
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
	subs := d.subscribers[event.Chain]
	d.mu.RUnlock()

	for _, ch := range subs {
		select {
		case ch <- event:
		default:
			// Backpressure protection:
			// Eğer subscriber doluysa bloklamıyoruz
			// İstersen burada log atabilirsin
		}
	}
}

func (d *Dispatcher) Shutdown() {
	d.cancel()

	d.mu.Lock()
	defer d.mu.Unlock()

	for _, subs := range d.subscribers {
		for _, ch := range subs {
			close(ch)
		}
	}

	d.wg.Wait()
}
