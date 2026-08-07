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

func TestListenerStyleDispatchCannotSucceedBeforePersistenceAck(t *testing.T) {
	bus := NewDispatcher()
	defer bus.Shutdown()
	sub := bus.Subscribe(constants.Ethereum, 1)
	persist := make(chan struct{})
	received := make(chan struct{})
	persistErr := errors.New("chain fact commit failed")

	go func() {
		event := <-sub
		close(received)
		<-persist
		event.Ack <- persistErr
	}()
	result := make(chan error, 1)
	go func() {
		result <- bus.DispatchAndWait(context.Background(), Event{Chain: constants.Ethereum})
	}()
	select {
	case <-received:
	case <-time.After(time.Second):
		t.Fatal("subscriber did not receive listener event")
	}

	select {
	case err := <-result:
		t.Fatalf("dispatch completed before durable persistence acknowledgement: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	close(persist)
	select {
	case err := <-result:
		if !errors.Is(err, persistErr) {
			t.Fatalf("error = %v, want persistence error", err)
		}
	case <-time.After(time.Second):
		t.Fatal("dispatch did not return after persistence acknowledgement")
	}
}

func TestDispatchAndWaitRequiresAckFromEverySubscriber(t *testing.T) {
	bus := NewDispatcher()
	bus.ackTimeout = 50 * time.Millisecond
	defer bus.Shutdown()
	first := bus.Subscribe(constants.Ethereum, 1)
	second := bus.Subscribe(constants.Ethereum, 1)

	go func() {
		event := <-first
		event.Ack <- nil
		// A duplicate acknowledgement from this subscriber must never satisfy
		// the second subscriber's acknowledgement requirement.
		event.Ack <- nil
	}()
	go func() {
		<-second
	}()

	err := bus.DispatchAndWait(context.Background(), Event{Chain: constants.Ethereum})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want acknowledgement deadline", err)
	}
}

func TestDispatchAndWaitBoundsMissingAckWithoutCallerDeadline(t *testing.T) {
	bus := NewDispatcher()
	bus.ackTimeout = 25 * time.Millisecond
	defer bus.Shutdown()
	sub := bus.Subscribe(constants.TRON, 1)
	go func() {
		<-sub
	}()

	started := time.Now()
	err := bus.DispatchAndWait(context.Background(), Event{Chain: constants.TRON})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want acknowledgement deadline", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("missing acknowledgement blocked for %s", elapsed)
	}
}

func TestDispatchAndWaitRejectsClosedAckChannel(t *testing.T) {
	bus := NewDispatcher()
	defer bus.Shutdown()
	sub := bus.Subscribe(constants.Solana, 1)
	go func() {
		event := <-sub
		close(event.Ack)
	}()

	err := bus.DispatchAndWait(context.Background(), Event{Chain: constants.Solana})
	if !errors.Is(err, ErrSubscriberAckClosed) {
		t.Fatalf("error = %v, want ErrSubscriberAckClosed", err)
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
