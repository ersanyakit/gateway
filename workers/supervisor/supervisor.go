package supervisor

import (
	"context"
	"core/helpers"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"
)

type RestartPolicy int

const (
	RestartNever RestartPolicy = iota
	RestartOnError
)

type Task struct {
	Name    string
	Run     func(context.Context) error
	Stop    func(context.Context) error
	Restart RestartPolicy
}

type TaskError struct {
	Name       string
	Err        error
	Restarting bool
}

type Options struct {
	RestartDelay time.Duration
	OnError      func(TaskError)
}

type Supervisor struct {
	options Options

	mu      sync.Mutex
	tasks   []Task
	started bool
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

func New(options Options) *Supervisor {
	if options.RestartDelay <= 0 {
		options.RestartDelay = time.Second
	}
	return &Supervisor{options: options}
}

func (s *Supervisor) Add(task Task) error {
	if s == nil {
		return errors.New("supervisor is nil")
	}
	if task.Name == "" {
		return errors.New("supervisor task name is required")
	}
	if task.Run == nil {
		return fmt.Errorf("supervisor task %q run function is required", task.Name)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return errors.New("cannot add supervisor task after start")
	}
	s.tasks = append(s.tasks, task)
	return nil
}

func (s *Supervisor) TaskNames() []string {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	names := make([]string, 0, len(s.tasks))
	for _, task := range s.tasks {
		names = append(names, task.Name)
	}
	return names
}

func (s *Supervisor) Start(parent context.Context) error {
	if s == nil {
		return errors.New("supervisor is nil")
	}
	if parent == nil {
		parent = context.Background()
	}

	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return errors.New("supervisor already started")
	}
	ctx, cancel := context.WithCancel(parent)
	s.cancel = cancel
	s.started = true
	tasks := append([]Task(nil), s.tasks...)
	s.mu.Unlock()

	for _, task := range tasks {
		task := task
		s.wg.Add(1)
		helpers.GoSafely("supervisor."+task.Name, func() {
			defer s.wg.Done()
			s.runTask(ctx, task)
		})
	}
	return nil
}

func (s *Supervisor) Stop(ctx context.Context) error {
	if s == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	s.mu.Lock()
	cancel := s.cancel
	tasks := append([]Task(nil), s.tasks...)
	s.mu.Unlock()

	if cancel != nil {
		cancel()
	}

	var errs []error
	for i := len(tasks) - 1; i >= 0; i-- {
		task := tasks[i]
		if task.Stop == nil {
			continue
		}
		if err := task.Stop(ctx); err != nil {
			errs = append(errs, fmt.Errorf("%s stop: %w", task.Name, err))
		}
	}

	done := make(chan struct{})
	helpers.GoSafely("supervisor.wait-for-workers", func() {
		s.wg.Wait()
		close(done)
	})
	select {
	case <-done:
	case <-ctx.Done():
		errs = append(errs, ctx.Err())
	}

	return errors.Join(errs...)
}

func (s *Supervisor) runTask(ctx context.Context, task Task) {
	for {
		err := runSafely(ctx, task)
		if ctx.Err() != nil {
			return
		}
		if err == nil {
			return
		}

		restarting := task.Restart == RestartOnError
		s.report(TaskError{Name: task.Name, Err: err, Restarting: restarting})
		if !restarting {
			return
		}

		timer := time.NewTimer(s.options.RestartDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func runSafely(ctx context.Context, task Task) error {
	var taskErr error
	panicErr := helpers.RunSafely("supervisor.task."+task.Name, func() {
		taskErr = task.Run(ctx)
	})
	if panicErr != nil {
		return panicErr
	}
	return taskErr
}

func (s *Supervisor) report(taskErr TaskError) {
	if s == nil {
		return
	}
	if s.options.OnError != nil {
		s.options.OnError(taskErr)
		return
	}
	log.Printf("supervisor task=%s restarting=%v error=%v", taskErr.Name, taskErr.Restarting, taskErr.Err)
}
