package supervisor

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSupervisorCancelsWorkersAndStopsInReverseOrder(t *testing.T) {
	s := New(Options{RestartDelay: time.Millisecond})
	var mu sync.Mutex
	var stopped []string
	firstDone := make(chan struct{})
	secondDone := make(chan struct{})

	if err := s.Add(Task{
		Name: "first",
		Run: func(ctx context.Context) error {
			<-ctx.Done()
			close(firstDone)
			return nil
		},
		Stop: func(context.Context) error {
			mu.Lock()
			defer mu.Unlock()
			stopped = append(stopped, "first")
			return nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Add(Task{
		Name: "second",
		Run: func(ctx context.Context) error {
			<-ctx.Done()
			close(secondDone)
			return nil
		},
		Stop: func(context.Context) error {
			mu.Lock()
			defer mu.Unlock()
			stopped = append(stopped, "second")
			return nil
		},
	}); err != nil {
		t.Fatal(err)
	}

	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.Stop(stopCtx); err != nil {
		t.Fatal(err)
	}
	<-firstDone
	<-secondDone

	mu.Lock()
	defer mu.Unlock()
	if want := []string{"second", "first"}; !reflect.DeepEqual(stopped, want) {
		t.Fatalf("stop order = %v, want %v", stopped, want)
	}
}

func TestSupervisorRestartsFailedTask(t *testing.T) {
	var runs atomic.Int32
	restarted := make(chan struct{})
	s := New(Options{
		RestartDelay: time.Millisecond,
		OnError: func(event TaskError) {
			if event.Name == "flaky" && event.Restarting {
				close(restarted)
			}
		},
	})
	if err := s.Add(Task{
		Name:    "flaky",
		Restart: RestartOnError,
		Run: func(ctx context.Context) error {
			if runs.Add(1) == 1 {
				return errors.New("boom")
			}
			<-ctx.Done()
			return nil
		},
	}); err != nil {
		t.Fatal(err)
	}

	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-restarted:
	case <-time.After(time.Second):
		t.Fatal("task did not report restart")
	}
	for runs.Load() < 2 {
		time.Sleep(time.Millisecond)
	}
	stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.Stop(stopCtx); err != nil {
		t.Fatal(err)
	}
}

func TestSupervisorReportsPanicAsTaskError(t *testing.T) {
	reported := make(chan TaskError, 1)
	s := New(Options{
		RestartDelay: time.Millisecond,
		OnError: func(event TaskError) {
			reported <- event
		},
	})
	if err := s.Add(Task{
		Name:    "panic-task",
		Restart: RestartNever,
		Run: func(context.Context) error {
			panic("bad task")
		},
	}); err != nil {
		t.Fatal(err)
	}

	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-reported:
		if event.Name != "panic-task" || event.Err == nil || event.Restarting {
			t.Fatalf("event = %#v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("panic was not reported")
	}
}

func TestSupervisorAddReturnsValidationError(t *testing.T) {
	s := New(Options{})
	if err := s.Add(Task{}); err == nil {
		t.Fatal("Add returned nil for an invalid task")
	}
}
