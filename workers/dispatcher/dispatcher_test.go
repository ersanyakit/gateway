package dispatcher

import (
	"context"
	"errors"
	"testing"
	"time"

	"core/constants"
)

func TestDispatchAndWaitDeliversAndWaitsForAck(t *testing.T) {
	bus := NewDispatcher()
	defer bus.Shutdown()
	sub := bus.Subscribe(constants.Ethereum, 1)

	done := make(chan Event, 1)
	go func() {
		event := <-sub
		event.Ack <- nil
		done <- event
	}()

	err := bus.DispatchAndWait(context.Background(), Event{Chain: constants.Ethereum, Type: "native_transfer"})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-done:
		if event.Type != "native_transfer" {
			t.Fatalf("event type = %q", event.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("subscriber did not receive event")
	}
}

func TestDispatchAndWaitNoSubscribers(t *testing.T) {
	bus := NewDispatcher()
	defer bus.Shutdown()
	err := bus.DispatchAndWait(context.Background(), Event{Chain: constants.Ethereum})
	if !errors.Is(err, ErrNoSubscribers) {
		t.Fatalf("error = %v, want ErrNoSubscribers", err)
	}
}

func TestDispatchAndWaitReturnsAckErrors(t *testing.T) {
	bus := NewDispatcher()
	defer bus.Shutdown()
	sub := bus.Subscribe(constants.Solana, 1)
	ackErr := errors.New("consumer failed")
	go func() {
		event := <-sub
		event.Ack <- ackErr
	}()

	err := bus.DispatchAndWait(context.Background(), Event{Chain: constants.Solana})
	if !errors.Is(err, ackErr) {
		t.Fatalf("error = %v, want ack error", err)
	}
}

func TestConcurrentDispatchAndUnsubscribeCannotSendOnClosedChannel(t *testing.T) {
	bus := NewDispatcher()
	defer bus.Shutdown()
	sub := bus.Subscribe(constants.Ethereum, 1)

	dispatched := make(chan struct{})
	go func() {
		defer close(dispatched)
		bus.Dispatch(Event{Chain: constants.Ethereum, Type: "native_transfer"})
	}()
	bus.Unsubscribe(constants.Ethereum, sub)

	select {
	case <-dispatched:
	case <-time.After(time.Second):
		t.Fatal("dispatch remained blocked during unsubscribe")
	}

	select {
	case <-sub:
		// A dispatch that already captured the subscription may complete once.
	default:
	}
	bus.Dispatch(Event{Chain: constants.Ethereum, Type: "after_unsubscribe"})
	select {
	case event := <-sub:
		t.Fatalf("received event after unsubscribe: %#v", event)
	default:
	}
}

func TestValidateScaleModeRejectsProductionScaleInProcessDispatcher(t *testing.T) {
	t.Setenv("APP_SCALE_MODE", "")
	t.Setenv("PRODUCTION_SCALE_MODE", "")
	bus := NewDispatcher()
	defer bus.Shutdown()
	if err := bus.ValidateScaleMode(); err != nil {
		t.Fatalf("default scale mode error = %v, want nil", err)
	}

	t.Setenv("APP_SCALE_MODE", "production")
	if err := bus.ValidateScaleMode(); !errors.Is(err, ErrDistributedBusRequired) {
		t.Fatalf("production scale mode error = %v, want ErrDistributedBusRequired", err)
	}
}
