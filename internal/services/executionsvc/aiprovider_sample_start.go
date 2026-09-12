package executionsvc

import (
	"time"

	"github.com/alicoding/mill/internal/domain/aiprovider"
	"github.com/alicoding/mill/internal/domain/composition"
)

func (e *ExecutionService) reserveAIProviderSampleRun(
	workflow composition.Workflow,
	nodes []composition.Node,
	edges []composition.Edge,
	attributes []composition.AttributeDef,
	kind RunKind,
	opts RunOptions,
	runID string,
) bool {
	workflow.Nodes = nodes
	workflow.Edges = edges
	workflow.Attributes = attributes
	_, reserved := reserveFreshAIProviderSampleAttempt(
		e.aiProviderSamples.observers,
		e.aiProviderSamples.capture,
		workflow,
		kind,
		opts,
		runID,
	)
	return reserved
}

func (e *ExecutionService) finishAIProviderSampleGenesis(runID string, reserved bool, genesisErr error) bool {
	if !reserved {
		return false
	}
	if genesisErr != nil {
		discardAIProviderSampleObservation(e.aiProviderSamples.observers, runID)
		return false
	}
	return activateAIProviderSampleObservation(e.aiProviderSamples.observers, runID, e.aiProviderSamples.activate)
}

func (e *ExecutionService) observeAIProviderSampleRun(runID string) {
	startAIProviderSampleObservation(
		e.aiProviderSamples.observers,
		e.ctx,
		runID,
		func(attempt aiprovider.SampleAttempt, outcome aiprovider.SampleOutcome, checkedAt time.Time) {
			if e.aiProviderSamples.settle != nil {
				e.aiProviderSamples.settle(attempt, outcome, checkedAt)
			}
		},
	)
}
