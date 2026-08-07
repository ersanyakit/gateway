package helpers

import (
	"bytes"
	"errors"
	"log"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRunSafelyRecoversAndLogsStack(t *testing.T) {
	var output bytes.Buffer
	logger := log.New(&output, "", 0)
	sentinel := errors.New("worker failed")

	err := runSafely(logger, "test-worker", func() {
		panic(sentinel)
	})
	if err == nil {
		t.Fatal("RunSafely returned nil after panic")
	}
	var recovered *RecoveredPanicError
	if !errors.As(err, &recovered) {
		t.Fatalf("RunSafely error = %T %v, want RecoveredPanicError", err, err)
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("RunSafely error = %v, want wrapped sentinel", err)
	}
	logged := output.String()
	if !strings.Contains(logged, `task="test-worker"`) ||
		!strings.Contains(logged, "worker failed") ||
		!strings.Contains(logged, "goroutine") {
		t.Fatalf("panic log is incomplete: %q", logged)
	}
}

func TestGoSafelyRestartingRecoversAndRunsAgainUntilStopped(t *testing.T) {
	stop := make(chan struct{})
	restarted := make(chan struct{}, 1)
	var runs atomic.Int32
	GoSafelyRestarting("restart-test", stop, time.Millisecond, func() {
		if runs.Add(1) == 1 {
			panic("restart me")
		}
		select {
		case restarted <- struct{}{}:
		default:
		}
		<-stop
	})
	select {
	case <-restarted:
		close(stop)
	case <-time.After(time.Second):
		close(stop)
		t.Fatal("restarting goroutine did not run again")
	}
	if runs.Load() < 2 {
		t.Fatalf("runs = %d, want at least 2", runs.Load())
	}
}

func TestRunSafelyRejectsNilFunctionAndLogs(t *testing.T) {
	var output bytes.Buffer
	err := runSafely(log.New(&output, "", 0), "nil-worker", nil)
	if !errors.Is(err, ErrNilSafeFunction) {
		t.Fatalf("RunSafely error = %v, want ErrNilSafeFunction", err)
	}
	if !strings.Contains(output.String(), "safe goroutine function is nil") {
		t.Fatalf("nil function error was not logged: %q", output.String())
	}
}

func TestGoSafelyWithDoneReportsRecoveredPanic(t *testing.T) {
	var output bytes.Buffer
	done := make(chan error, 1)
	goSafely(log.New(&output, "", 0), "async-worker", func() {
		panic("async failure")
	}, func(err error) {
		done <- err
	})

	select {
	case err := <-done:
		var recovered *RecoveredPanicError
		if !errors.As(err, &recovered) {
			t.Fatalf("completion error = %T %v, want RecoveredPanicError", err, err)
		}
	case <-time.After(time.Second):
		t.Fatal("safe goroutine completion timed out")
	}
	if !strings.Contains(output.String(), "async failure") {
		t.Fatalf("async panic was not logged: %q", output.String())
	}
}
