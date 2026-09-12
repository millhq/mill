package executionsvc

import (
	"errors"
	"testing"
	"time"

	"github.com/alicoding/mill/internal/adapters/execution"
	"github.com/alicoding/mill/internal/domain/aiprovider"
	"github.com/alicoding/mill/internal/domain/composition"
)

func TestAIProviderSampleObserverStopLatchesBeforeGenesisFinishes(t *testing.T) {
	state := newAIProviderSampleObserverState()
	attempt := aiprovider.SampleAttempt{RunID: "racing-genesis"}
	reserveSampleObservationForTest(t, state, attempt)

	stopped := make(chan struct{})
	go func() {
		stopAIProviderSampleObservers(state)
		close(stopped)
	}()

	deadline := time.Now().Add(time.Second)
	for {
		state.mu.Lock()
		latched := state.stopping
		state.mu.Unlock()
		if latched {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("stop did not latch")
		}
	}
	select {
	case <-stopped:
		t.Fatal("stop returned before the reserved genesis settled")
	default:
	}

	discardAIProviderSampleObservation(state, attempt.RunID)
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("stop did not join the reserved genesis")
	}
	if _, ok := reserveFreshAIProviderSampleAttempt(state, func(composition.Workflow, string, map[string]string, string) (aiprovider.SampleAttempt, bool) {
		return aiprovider.SampleAttempt{RunID: "after-stop"}, true
	}, composition.Workflow{}, RunKindTest, RunOptions{}, "after-stop"); ok {
		t.Fatal("reservation succeeded after stop returned")
	}
}

func TestAIProviderSampleTerminalOutcomeRequiresTerminalPersistedStatus(t *testing.T) {
	tests := []struct {
		name   string
		status execution.WorkflowStatus
		want   aiprovider.SampleOutcome
		ok     bool
	}{
		{name: "success", status: execution.WorkflowStatus{Status: execution.WorkflowStatusSuccess}, want: aiprovider.SampleOutcomeSucceeded, ok: true},
		{name: "error", status: execution.WorkflowStatus{Status: execution.WorkflowStatusError}, want: aiprovider.SampleOutcomeFailed, ok: true},
		{name: "exhausted", status: execution.WorkflowStatus{Status: execution.WorkflowStatusMaxRecoveryAttemptsExceeded}, want: aiprovider.SampleOutcomeFailed, ok: true},
		{name: "cancelled", status: execution.WorkflowStatus{Status: execution.WorkflowStatusCancelled}, want: aiprovider.SampleOutcomeCancelled, ok: true},
		{name: "pending", status: execution.WorkflowStatus{Status: execution.WorkflowStatusPending}, ok: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := aiProviderSampleTerminalOutcome(tt.status)
			if ok != tt.ok || got != tt.want {
				t.Fatalf("aiProviderSampleTerminalOutcome = (%q, %t), want (%q, %t)", got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestAIProviderSampleObservedOutcomeDistinguishesWaitFailure(t *testing.T) {
	waitErr := errors.New("wait failed")
	if _, ok := aiProviderSampleObservedOutcome(execution.WorkflowStatus{Status: execution.WorkflowStatusSuccess}, waitErr); ok {
		t.Fatal("successful status with failed observation produced evidence")
	}
	for _, tt := range []struct {
		status execution.WorkflowStatusType
		want   aiprovider.SampleOutcome
	}{
		{status: execution.WorkflowStatusError, want: aiprovider.SampleOutcomeFailed},
		{status: execution.WorkflowStatusCancelled, want: aiprovider.SampleOutcomeCancelled},
	} {
		got, ok := aiProviderSampleObservedOutcome(execution.WorkflowStatus{Status: tt.status}, waitErr)
		if !ok || got != tt.want {
			t.Fatalf("aiProviderSampleObservedOutcome(%q) = (%q, %t), want (%q, true)", tt.status, got, ok, tt.want)
		}
	}
}

func TestAIProviderSamplePersistedOutputUsesExactAINodeStep(t *testing.T) {
	node := sampleWorkflowForTest(aiprovider.OperationStructured).Nodes[0]
	want := composition.ExecContext{Attributes: map[string]any{"item": "tea", "quantity": float64(2), "inStock": true}}
	got, ok := aiProviderSamplePersistedOutput(execution.WorkflowStatus{}, node, func() ([]execution.StepInfo, error) {
		return []execution.StepInfo{
			{StepName: "another-step", Output: `{}`},
			{StepID: 2, StepName: node.ID, Output: `{"Payload":"","Attributes":{"item":"stale","quantity":1,"inStock":false}}`},
			{StepID: 3, StepName: node.ID, Output: `{"Payload":"","Attributes":{"item":"tea","quantity":2,"inStock":true}}`},
		}, nil
	})
	if !ok || got.Attributes["item"] != want.Attributes["item"] || got.Attributes["quantity"] != want.Attributes["quantity"] || got.Attributes["inStock"] != want.Attributes["inStock"] {
		t.Fatalf("aiProviderSamplePersistedOutput = (%+v, %t), want %+v", got, ok, want)
	}
}

func TestAIProviderSamplePersistedOutputRejectsObserverAndStoredStepFailures(t *testing.T) {
	node := sampleWorkflowForTest(aiprovider.OperationClassification).Nodes[0]
	tests := []struct {
		name string
		load func() ([]execution.StepInfo, error)
	}{
		{name: "observer query", load: func() ([]execution.StepInfo, error) { return nil, errors.New("database unavailable") }},
		{name: "missing step", load: func() ([]execution.StepInfo, error) { return nil, nil }},
		{name: "step error", load: func() ([]execution.StepInfo, error) {
			return []execution.StepInfo{{StepName: node.ID, Error: errors.New("provider failed")}}, nil
		}},
		{name: "malformed output", load: func() ([]execution.StepInfo, error) {
			return []execution.StepInfo{{StepName: node.ID, Output: `{`}}, nil
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, ok := aiProviderSamplePersistedOutput(execution.WorkflowStatus{}, node, tt.load); ok {
				t.Fatal("aiProviderSamplePersistedOutput accepted an unobservable result")
			}
		})
	}
}

func TestDiscardAIProviderSampleObservationIsIdempotent(t *testing.T) {
	state := newAIProviderSampleObserverState()
	reserveSampleObservationForTest(t, state, aiprovider.SampleAttempt{RunID: "run-1"})
	discardAIProviderSampleObservation(state, "run-1")
	discardAIProviderSampleObservation(state, "run-1")
	stopAIProviderSampleObservers(state)
}

func TestAIProviderSampleObservationRequiresSuccessfulActivation(t *testing.T) {
	state := newAIProviderSampleObserverState()
	attempt := aiprovider.SampleAttempt{RunID: "run-1"}
	reserveSampleObservationForTest(t, state, attempt)
	if activateAIProviderSampleObservation(state, attempt.RunID, func(aiprovider.SampleAttempt) bool { return false }) {
		t.Fatal("activation returned true after Configure refused it")
	}
	state.mu.Lock()
	_, exists := state.observations[attempt.RunID]
	state.mu.Unlock()
	if exists {
		t.Fatal("failed activation left an observation reservation")
	}
	stopAIProviderSampleObservers(state)
}

func reserveSampleObservationForTest(t *testing.T, state *aiProviderSampleObserverState, attempt aiprovider.SampleAttempt) {
	t.Helper()
	if attempt.Operation == "" {
		attempt.Operation = aiprovider.OperationText
	}
	capture := func(composition.Workflow, string, map[string]string, string) (aiprovider.SampleAttempt, bool) {
		return attempt, true
	}
	if _, ok := reserveFreshAIProviderSampleAttempt(state, capture, sampleWorkflowForTest(attempt.Operation), RunKindTest, RunOptions{}, attempt.RunID); !ok {
		t.Fatal("reserveFreshAIProviderSampleAttempt returned false")
	}
}

func sampleWorkflowForTest(operation aiprovider.Operation) composition.Workflow {
	node := composition.Node{ID: "provider-sample-ai", Config: map[string]string{}}
	switch operation {
	case aiprovider.OperationStructured:
		node.NodeTypeID = "process-ai-extract-structured"
		node.Config["outputFields"] = `[{"Key":"item","Type":"text"},{"Key":"quantity","Type":"integer"},{"Key":"inStock","Type":"boolean"}]`
	case aiprovider.OperationClassification:
		node.NodeTypeID = "process-ai-classify"
		node.Config["categories"] = "urgent\nnormal"
		node.Config["outputAttribute"] = "category"
	default:
		node.NodeTypeID = "process-ai-completion"
	}
	return composition.Workflow{ID: "sample-workflow", Nodes: []composition.Node{node}}
}
