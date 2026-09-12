package executionsvc

import (
	"sync/atomic"
	"testing"

	"github.com/alicoding/mill/internal/domain/aiprovider"
	"github.com/alicoding/mill/internal/domain/composition"
)

func TestReserveFreshAIProviderSampleAttemptEligibility(t *testing.T) {
	workflow := sampleWorkflowForTest(aiprovider.OperationText)
	capture := func(_ composition.Workflow, payload string, values map[string]string, runID string) (aiprovider.SampleAttempt, bool) {
		if payload != "" || len(values) != 0 {
			return aiprovider.SampleAttempt{}, false
		}
		return aiprovider.SampleAttempt{RunID: runID, WorkflowID: workflow.ID, Operation: aiprovider.OperationText}, true
	}
	tests := []struct {
		name string
		kind RunKind
		opts RunOptions
		want bool
	}{
		{name: "ordinary UI run", kind: RunKindTest, want: true},
		{name: "ordinary MCP run", kind: RunKindMCP, want: true},
		{name: "triggered run", kind: RunKindTriggered},
		{name: "stepped debug run", kind: RunKindTest, opts: RunOptions{Stepped: true}},
		{name: "payload override", kind: RunKindTest, opts: RunOptions{Payload: "override"}},
		{name: "value override", kind: RunKindMCP, opts: RunOptions{Values: map[string]string{"x": "override"}}},
		{name: "atlas run", kind: RunKindTest, opts: RunOptions{AtlasSourceCardID: "card-1"}},
		{name: "typed secret run", kind: RunKindTest, opts: RunOptions{SecretsToken: "token"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := newAIProviderSampleObserverState()
			attempt, ok := reserveFreshAIProviderSampleAttempt(state, capture, workflow, tt.kind, tt.opts, "run-1")
			if ok != tt.want {
				t.Fatalf("reserveFreshAIProviderSampleAttempt = (%+v, %t), want %t", attempt, ok, tt.want)
			}
			if ok {
				discardAIProviderSampleObservation(state, attempt.RunID)
			}
			stopAIProviderSampleObservers(state)
		})
	}
}

func TestReserveFreshAIProviderSampleAttemptCreatesNoTokenAfterStopLatch(t *testing.T) {
	state := newAIProviderSampleObserverState()
	stopAIProviderSampleObservers(state)
	var captures atomic.Int32
	capture := func(_ composition.Workflow, _ string, _ map[string]string, runID string) (aiprovider.SampleAttempt, bool) {
		captures.Add(1)
		return aiprovider.SampleAttempt{RunID: runID}, true
	}
	if _, ok := reserveFreshAIProviderSampleAttempt(state, capture, composition.Workflow{}, RunKindTest, RunOptions{}, "run-1"); ok {
		t.Fatal("reservation succeeded after stop")
	}
	if captures.Load() != 0 {
		t.Fatal("capture callback created a token after stop latch")
	}
}

func TestReserveFreshAIProviderSampleAttemptRejectsMismatchedRunID(t *testing.T) {
	state := newAIProviderSampleObserverState()
	capture := func(_ composition.Workflow, _ string, _ map[string]string, _ string) (aiprovider.SampleAttempt, bool) {
		return aiprovider.SampleAttempt{RunID: "different-run"}, true
	}
	if _, ok := reserveFreshAIProviderSampleAttempt(state, capture, composition.Workflow{}, RunKindTest, RunOptions{}, "run-1"); ok {
		t.Fatal("reservation accepted a token for another run")
	}
	stopAIProviderSampleObservers(state)
}
