package application

import (
	"strings"
	"testing"

	"core/api/routes"
	webhooksvc "core/services/webhook"
	"core/workers/dispatcher"
	"core/workers/supervisor"
)

func TestCompositionRootRequiresRuntimeDependencies(t *testing.T) {
	root, err := NewCompositionRoot(
		&App{Router: &routes.Router{}},
		dispatcher.NewDispatcher(),
		webhooksvc.NewNotifier(),
		supervisor.New(supervisor.Options{}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if root.App == nil || root.Dispatcher == nil || root.WebhookNotifier == nil || root.WorkerSupervisor == nil {
		t.Fatalf("composition root missing dependency: %#v", root)
	}
}

func TestCompositionRootRejectsMissingDependency(t *testing.T) {
	_, err := NewCompositionRoot(&App{Router: &routes.Router{}}, nil, webhooksvc.NewNotifier(), supervisor.New(supervisor.Options{}))
	if err == nil || !strings.Contains(err.Error(), "dispatcher") {
		t.Fatalf("err = %v, want dispatcher validation", err)
	}
}
