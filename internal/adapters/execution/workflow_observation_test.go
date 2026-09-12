package execution

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestCancelledObservationDoesNotCancelDurableWorkflow(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})
	workflow := func(_ Context, input string) (string, error) {
		if input == "" {
			return "", fmt.Errorf("observation fixture needs input")
		}
		close(started)
		<-release
		return "persisted output", nil
	}

	runtime, err := New("mill-observation-test", "1", "sqlite:"+filepath.Join(t.TempDir(), "observation.db"), func(ctx Context) {
		RegisterWorkflow(ctx, workflow)
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = Shutdown(runtime, 5*time.Second) })
	var releaseOnce sync.Once
	releaseWorkflow := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(releaseWorkflow)

	handle, err := RunWorkflow(runtime, workflow, "input", WithWorkflowID("observed-run"))
	if err != nil {
		t.Fatalf("RunWorkflow: %v", err)
	}
	t.Cleanup(func() {
		releaseWorkflow()
		terminal := make(chan struct{})
		go func() {
			_, _ = handle.GetResult()
			close(terminal)
		}()
		select {
		case <-terminal:
		case <-time.After(5 * time.Second):
			t.Error("workflow did not reach terminal status before runtime teardown")
		}
	})
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("workflow did not start")
	}

	observerContext, cancelObserver := WithCancel(runtime)
	observer, err := RetrieveWorkflow[string](observerContext, handle.GetWorkflowID())
	if err != nil {
		t.Fatalf("RetrieveWorkflow: %v", err)
	}
	observed := make(chan error, 1)
	go func() {
		_, waitErr := observer.GetResult()
		observed <- waitErr
	}()
	cancelObserver()
	select {
	case waitErr := <-observed:
		if waitErr == nil {
			t.Fatal("cancelled observer GetResult returned nil error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled observer did not return")
	}

	releaseWorkflow()
	type resultAndError struct {
		result string
		err    error
	}
	finished := make(chan resultAndError, 1)
	go func() {
		result, resultErr := handle.GetResult()
		finished <- resultAndError{result: result, err: resultErr}
	}()
	var result string
	select {
	case got := <-finished:
		if got.err != nil {
			t.Fatalf("durable workflow after observer cancellation: %v", got.err)
		}
		result = got.result
	case <-time.After(5 * time.Second):
		t.Fatal("durable workflow did not reach terminal status")
	}
	if result != "persisted output" {
		t.Errorf("durable workflow result = %q, want persisted output", result)
	}

	statusContext, cancelStatus := WithTimeout(runtime, 5*time.Second)
	defer cancelStatus()
	status, err := WorkflowByID(statusContext, handle.GetWorkflowID(), true)
	if err != nil {
		t.Fatalf("WorkflowByID: %v", err)
	}
	if status.Status != WorkflowStatusSuccess {
		t.Errorf("status = %q, want %q", status.Status, WorkflowStatusSuccess)
	}
}

func TestRetrieveWorkflowRejectsNilClient(t *testing.T) {
	if _, err := RetrieveWorkflow[string](nil, "missing"); err == nil {
		t.Fatal("RetrieveWorkflow(nil) returned nil error")
	}
}

var _ context.Context = (Context)(nil)
