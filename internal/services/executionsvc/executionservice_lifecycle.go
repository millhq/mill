package executionsvc

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/alicoding/mill/internal/adapters/dataownership"
	"github.com/alicoding/mill/internal/adapters/execution"
	"github.com/alicoding/mill/internal/domain/composition"
	"github.com/alicoding/mill/internal/services/compositionsvc"
	"github.com/alicoding/mill/internal/services/guardrailsvc"
)

// The durable runtime's lifecycle: prepare, host wiring, launch, and shutdown.
// Split from executionservice.go (which keeps the run/step shapes and the
// workflow body) at the 500-line limit, along the same "one file owns
// one concern end to end" seam executionservice_cancel.go established.

// StartupWiring installs host-owned callbacks between preparation and launch.
// Production prepares and wires explicitly; this option preserves the compact
// construct-and-launch API for standalone callers.
type StartupWiring func(*ExecutionService)

// NewExecutionService builds and launches the durable-execution runtime
// backed by databaseURL (a DBOS-native DSN -- see execution.Prepare's own
// doc comment for the sqlite-by-default, Postgres-by-config reasoning).
// Registration happens during preparation, before Launch, per that adapter's
// own doc comment.
func NewExecutionService(databaseURL string, comp *compositionsvc.CompositionService, guard *guardrailsvc.GuardrailService) (*ExecutionService, error) {
	return NewExecutionServiceWithVersionAndOwnership(databaseURL, WorkflowCodeVersion, dataownership.ExecutionOwnershipUnestablished, comp, guard)
}

// NewExecutionServiceWithOwnership supplies the startup-owned execution-store
// classification used by provider mutation safety.
func NewExecutionServiceWithOwnership(databaseURL string, ownership dataownership.ExecutionOwnership, comp *compositionsvc.CompositionService, guard *guardrailsvc.GuardrailService, startupWiring ...StartupWiring) (*ExecutionService, error) {
	return NewExecutionServiceWithVersionAndOwnership(databaseURL, WorkflowCodeVersion, ownership, comp, guard, startupWiring...)
}

// PrepareExecutionServiceWithOwnership constructs and registers the durable
// runtime without launching recovery. The composition root uses this boundary
// to install every body-facing dependency before LaunchExecutionService.
func PrepareExecutionServiceWithOwnership(databaseURL string, ownership dataownership.ExecutionOwnership, comp *compositionsvc.CompositionService, guard *guardrailsvc.GuardrailService) (*ExecutionService, error) {
	return PrepareExecutionServiceWithVersionAndOwnership(databaseURL, WorkflowCodeVersion, ownership, comp, guard)
}

// NewExecutionServiceWithVersion is NewExecutionService with the
// durable runtime's application version supplied explicitly -- the seam
// a test needs to relaunch the SAME database under a different
// workflow-code version (see WorkflowCodeVersion). Production always
// passes WorkflowCodeVersion.
func NewExecutionServiceWithVersion(databaseURL, appVersion string, comp *compositionsvc.CompositionService, guard *guardrailsvc.GuardrailService) (*ExecutionService, error) {
	return NewExecutionServiceWithVersionAndOwnership(databaseURL, appVersion, dataownership.ExecutionOwnershipUnestablished, comp, guard)
}

// NewExecutionServiceWithVersionAndOwnership is the compact prepare-and-launch
// constructor used by standalone callers and tests.
func NewExecutionServiceWithVersionAndOwnership(databaseURL, appVersion string, ownership dataownership.ExecutionOwnership, comp *compositionsvc.CompositionService, guard *guardrailsvc.GuardrailService, startupWiring ...StartupWiring) (*ExecutionService, error) {
	e, err := PrepareExecutionServiceWithVersionAndOwnership(databaseURL, appVersion, ownership, comp, guard)
	if err != nil {
		return nil, err
	}
	for _, wire := range startupWiring {
		if wire != nil {
			wire(e)
		}
	}
	if err := LaunchExecutionService(e); err != nil {
		return nil, err
	}
	return e, nil
}

// PrepareExecutionServiceWithVersionAndOwnership is the complete preparation
// boundary used by production and durable-recovery tests.
func PrepareExecutionServiceWithVersionAndOwnership(databaseURL, appVersion string, ownership dataownership.ExecutionOwnership, comp *compositionsvc.CompositionService, guard *guardrailsvc.GuardrailService) (*ExecutionService, error) {
	e := &ExecutionService{
		comp: comp, guard: guard, cancelState: newCancelState(), appVersion: appVersion,
		aiProviderMutation: newAIProviderMutationState(ownership),
		aiProviderSamples:  aiProviderSampleRuntime{observers: newAIProviderSampleObserverState()},
	}
	ctx, err := execution.Prepare("mill", appVersion, databaseURL, func(ctx execution.Context) {
		composition.SetGuardrailGate(e.guardrailGate)
		composition.SetApprovalWaiter(e.approvalWaiter)
		composition.SetProcessRegistrar(e.registerProcess)
		composition.SetShellStepProgressEmitter(e.emitShellStepProgress)
		composition.SetRunEvidenceLookup(e.runEvidenceLookup)
		composition.SetCurrentRunIDLookup(e.CurrentRunID)
		composition.SetChildWorkflowRunner(e.runChildWorkflow)
		execution.RegisterWorkflow(ctx, e.runWorkflow, execution.WithWorkflowName(millRunWorkflowName))
	})
	if err != nil {
		return nil, err
	}
	e.ctx = ctx
	return e, nil
}

// LaunchExecutionService begins queue processing and durable recovery only
// after the composition root has installed every dependency a body can read.
func LaunchExecutionService(e *ExecutionService) error {
	if e == nil || e.ctx == nil {
		return fmt.Errorf("execution service is not prepared")
	}
	if err := execution.Launch(e.ctx); err != nil {
		return err
	}
	// Runs stranded by an earlier build's application version are
	// settled here, once, immediately after Launch -- before any surface
	// can offer Resume/Approve on a run nothing will ever answer
	// (goal 0329).
	if err := e.ReconcileInterrupted(); err != nil {
		slog.Error("execution: reconcile interrupted runs", "error", err)
	}
	return nil
}

// Shutdown stops the durable-execution runtime -- called from main.go on
// application shutdown so in-flight step checkpoints flush cleanly.
//
//wails:ignore
func (e *ExecutionService) Shutdown(timeout time.Duration) error {
	stopAIProviderSampleObservers(e.aiProviderSamples.observers)
	return execution.Shutdown(e.ctx, timeout)
}
