package executionsvc

import (
	"time"

	"github.com/alicoding/mill/internal/domain/aiprovider"
	"github.com/alicoding/mill/internal/domain/composition"
)

type aiProviderSampleAttemptCapture func(composition.Workflow, string, map[string]string, string) (aiprovider.SampleAttempt, bool)

type aiProviderSampleRuntime struct {
	observers *aiProviderSampleObserverState
	capture   aiProviderSampleAttemptCapture
	activate  func(aiprovider.SampleAttempt) bool
	settle    func(aiprovider.SampleAttempt, aiprovider.SampleOutcome, time.Time) bool
}

// SetAIProviderSampleRuntime wires session-local evidence callbacks without
// exporting them through Wails service reflection.
func SetAIProviderSampleRuntime(
	e *ExecutionService,
	capture aiProviderSampleAttemptCapture,
	activate func(aiprovider.SampleAttempt) bool,
	settle func(aiprovider.SampleAttempt, aiprovider.SampleOutcome, time.Time) bool,
) {
	if e == nil {
		return
	}
	e.aiProviderSamples.capture = capture
	e.aiProviderSamples.activate = activate
	e.aiProviderSamples.settle = settle
}

// reserveFreshAIProviderSampleAttempt is the one eligibility door intended for
// runWorkflowStart after preflight and before DBOS genesis. Redrive, fork,
// recovery, child genesis and the generic workflow body do not call it.
func reserveFreshAIProviderSampleAttempt(
	state *aiProviderSampleObserverState,
	capture aiProviderSampleAttemptCapture,
	workflow composition.Workflow,
	kind RunKind,
	opts RunOptions,
	runID string,
) (aiprovider.SampleAttempt, bool) {
	if state == nil || capture == nil || runID == "" || (kind != RunKindTest && kind != RunKindMCP) ||
		opts.Stepped || opts.AtlasSourceCardID != "" || opts.SecretsToken != "" || opts.Responder != nil {
		return aiprovider.SampleAttempt{}, false
	}

	// Holding the observer lock across capture makes shutdown ordering exact:
	// Stop either latches first and no token is created, or this reservation is
	// visible to Stop's join before it can latch.
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.stopping {
		return aiprovider.SampleAttempt{}, false
	}
	attempt, ok := capture(workflow, opts.Payload, opts.Values, runID)
	if !ok || attempt.RunID != runID {
		return aiprovider.SampleAttempt{}, false
	}
	outputNode, err := aiProviderSampleOutputNode(workflow, attempt.Operation)
	if err != nil {
		return aiprovider.SampleAttempt{}, false
	}
	if _, exists := state.observations[runID]; exists {
		return aiprovider.SampleAttempt{}, false
	}
	state.workers.Add(1)
	state.observations[runID] = &aiProviderSampleObservation{attempt: attempt, outputNode: outputNode}
	return attempt, true
}
