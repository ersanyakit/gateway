package dispatcher

import (
	"context"
	"core/constants"
	"core/types"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

var (
	ErrNoSubscribers          = errors.New("no subscribers for chain")
	ErrDistributedBusRequired = errors.New("in-process dispatcher is not a distributed event bus")
	ErrSubscriberAckClosed    = errors.New("subscriber closed acknowledgement channel")
)

const defaultAckTimeout = 30 * time.Second

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
	ackTimeout  time.Duration
}

func NewDispatcher() *Dispatcher {
	ctx, cancel := context.WithCancel(context.Background())

	return &Dispatcher{
		subscribers: make(map[constants.ChainID][]chan Event),
		ctx:         ctx,
		cancel:      cancel,
		ackTimeout:  configuredAckTimeout(),
	}
}

func configuredAckTimeout() time.Duration {
	for _, key := range []string{"CHAIN_EVENT_ACK_TIMEOUT", "DISPATCHER_ACK_TIMEOUT"} {
		raw := strings.TrimSpace(os.Getenv(key))
		if raw == "" {
			continue
		}
		timeout, err := time.ParseDuration(raw)
		if err == nil && timeout > 0 {
			return timeout
		}
	}
	return defaultAckTimeout
}

func ProductionScaleModeEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("APP_SCALE_MODE"))) {
	case "production", "prod", "distributed":
		return true
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv("PRODUCTION_SCALE_MODE"))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func (d *Dispatcher) ValidateScaleMode() error {
	if ProductionScaleModeEnabled() {
		return ErrDistributedBusRequired
	}
	return nil
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
	ctx, cancel := d.boundedContext(ctx)
	defer cancel()

	d.mu.RLock()
	subs := append([]chan Event(nil), d.subscribers[event.Chain]...)
	d.mu.RUnlock()

	if len(subs) == 0 {
		return ErrNoSubscribers
	}

	// Each subscriber gets its own acknowledgement channel. A shared channel
	// lets a buggy subscriber acknowledge more than once and accidentally
	// satisfy another subscriber's acknowledgement requirement.
	acks := make([]<-chan error, 0, len(subs))
	for index, ch := range subs {
		delivered := event
		ack := make(chan error, 1)
		delivered.Ack = ack
		select {
		case ch <- delivered:
			acks = append(acks, ack)
		case <-ctx.Done():
			return fmt.Errorf("dispatch chain=%d subscriber=%d enqueue: %w", event.Chain, index, ctx.Err())
		case <-d.ctx.Done():
			return d.ctx.Err()
		}
	}

	var errs []error
	for index, ack := range acks {
		select {
		case err, ok := <-ack:
			if !ok {
				errs = append(errs, fmt.Errorf("dispatch chain=%d subscriber=%d: %w", event.Chain, index, ErrSubscriberAckClosed))
				continue
			}
			if err != nil {
				errs = append(errs, fmt.Errorf("dispatch chain=%d subscriber=%d acknowledgement: %w", event.Chain, index, err))
			}
		case <-ctx.Done():
			return fmt.Errorf("dispatch chain=%d subscriber=%d acknowledgement: %w", event.Chain, index, ctx.Err())
		case <-d.ctx.Done():
			return d.ctx.Err()
		}
	}

	return errors.Join(errs...)
}

func (d *Dispatcher) boundedContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	if d == nil || d.ackTimeout <= 0 {
		return context.WithCancel(ctx)
	}
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) <= d.ackTimeout {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, d.ackTimeout)
}

func (d *Dispatcher) Shutdown() {
	d.cancel()

	d.wg.Wait()
}
