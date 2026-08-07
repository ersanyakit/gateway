package helpers

import (
	"errors"
	"fmt"
	"log"
	"runtime/debug"
	"strings"
	"time"
)

var ErrNilSafeFunction = errors.New("safe goroutine function is nil")

// RecoveredPanicError reports a panic that was contained at a goroutine
// boundary. The original value is retained for errors.Is/errors.As callers;
// the full value and stack are also written to the configured logger.
type RecoveredPanicError struct {
	Task  string
	Value any
}

func (e *RecoveredPanicError) Error() string {
	if e == nil {
		return "panic recovered"
	}
	return fmt.Sprintf("%s panic recovered: %v", e.Task, e.Value)
}

func (e *RecoveredPanicError) Unwrap() error {
	if e == nil {
		return nil
	}
	err, _ := e.Value.(error)
	return err
}

// RunSafely executes fn synchronously, converts a panic into an error, and
// logs the recovered value together with its stack trace.
func RunSafely(name string, fn func()) error {
	return runSafely(log.Default(), name, fn)
}

func runSafely(logger *log.Logger, name string, fn func()) (err error) {
	if logger == nil {
		logger = log.Default()
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = "unnamed"
	}
	if fn == nil {
		err = fmt.Errorf("%s: %w", name, ErrNilSafeFunction)
		logger.Printf("goroutine task=%q error=%v", name, err)
		return err
	}

	defer func() {
		if recovered := recover(); recovered != nil {
			err = &RecoveredPanicError{Task: name, Value: recovered}
			logger.Printf(
				"goroutine task=%q recovered_panic_type=%T recovered_panic=%v\n%s",
				name,
				recovered,
				recovered,
				debug.Stack(),
			)
		}
	}()
	fn()
	return nil
}

// GoSafely starts fn in a goroutine protected by RunSafely.
func GoSafely(name string, fn func()) {
	goSafely(log.Default(), name, fn, nil)
}

// GoSafelyWithDone behaves like GoSafely and invokes done with the recovered
// panic error (or nil after normal completion). The completion callback is
// protected by the same panic boundary.
func GoSafelyWithDone(name string, fn func(), done func(error)) {
	goSafely(log.Default(), name, fn, done)
}

// GoSafelyRestarting keeps a long-running goroutine alive after a recovered
// panic or an unexpected return. Closing stop is the only normal terminal
// condition. This is intended for chain scanners and other durable polling
// loops where a silently dead goroutine would create an ingestion gap.
func GoSafelyRestarting(name string, stop <-chan struct{}, restartDelay time.Duration, fn func()) {
	if restartDelay <= 0 {
		restartDelay = time.Second
	}
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
			}

			err := RunSafely(name, fn)
			select {
			case <-stop:
				return
			default:
			}
			if err != nil {
				log.Printf("goroutine task=%q restarting_after=%s error=%v", name, restartDelay, err)
			} else {
				log.Printf("goroutine task=%q returned unexpectedly; restarting_after=%s", name, restartDelay)
			}
			timer := time.NewTimer(restartDelay)
			select {
			case <-stop:
				timer.Stop()
				return
			case <-timer.C:
			}
		}
	}()
}

func goSafely(logger *log.Logger, name string, fn func(), done func(error)) {
	go func() {
		err := runSafely(logger, name, fn)
		if done != nil {
			_ = runSafely(logger, name+".completion", func() {
				done(err)
			})
		}
	}()
}
