package application

import (
	"errors"

	webhooksvc "core/services/webhook"
	"core/workers/dispatcher"
	"core/workers/supervisor"
)

type CompositionRoot struct {
	App              *App
	Dispatcher       *dispatcher.Dispatcher
	WebhookNotifier  *webhooksvc.Notifier
	WorkerSupervisor *supervisor.Supervisor
}

func NewCompositionRoot(app *App, bus *dispatcher.Dispatcher, notifier *webhooksvc.Notifier, workerSupervisor *supervisor.Supervisor) (*CompositionRoot, error) {
	root := &CompositionRoot{
		App:              app,
		Dispatcher:       bus,
		WebhookNotifier:  notifier,
		WorkerSupervisor: workerSupervisor,
	}
	if err := root.Validate(); err != nil {
		return nil, err
	}
	return root, nil
}

func (r *CompositionRoot) Validate() error {
	if r == nil {
		return errors.New("composition root is nil")
	}
	if r.App == nil {
		return errors.New("composition root app is required")
	}
	if r.App.Router == nil {
		return errors.New("composition root router is required")
	}
	if r.Dispatcher == nil {
		return errors.New("composition root dispatcher is required")
	}
	if r.WebhookNotifier == nil {
		return errors.New("composition root webhook notifier is required")
	}
	if r.WorkerSupervisor == nil {
		return errors.New("composition root worker supervisor is required")
	}
	return nil
}
