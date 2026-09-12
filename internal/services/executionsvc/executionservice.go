// Package executionsvc is the Wails-facing layer that runs a workflow:
// durable execution via DBOS (internal/adapters/execution), the
// guardrail gate checked before every guarded step (guardrailsvc), run
// history/receipts, and cancellation. It owns orchestration state a
// stateless domain package can't hold; per-node-type execution
// semantics live in internal/domain/composition.
package executionsvc

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/alicoding/mill/internal/adapters/execution"
	"github.com/alicoding/mill/internal/domain/composition"
	"github.com/alicoding/mill/internal/services/compositionsvc"
	"github.com/alicoding/mill/internal/services/dataevent"
	"github.com/alicoding/mill/internal/services/guardrailsvc"
	"github.com/google/uuid"
)

// decodeAny re-decodes a DBOS-stored `any` value into T. Verified
// directly against a real run (not assumed from the Serializer
// interface's doc comments alone): WorkflowStatus.Input/Output and
// StepInfo.Output all come back as the raw JSON *string* DBOS's default
// serializer stored, not an already-decoded Go value -- a direct type
// assertion to T fails even though the data is really there, and so
// would blindly re-marshaling a string (json.Marshal on a Go string
// wraps it in quotes, corrupting the payload before Unmarshal ever
// sees it). The nil/non-string branch is defensive for any future
// caller that hands this a value DBOS already decoded.
func decodeAny[T any](v any) (T, bool) {
	var zero, out T
	if v == nil {
		return zero, false
	}
	raw, ok := v.(string)
	if !ok {
		rawBytes, err := json.Marshal(v)
		if err != nil {
			return zero, false
		}
		raw = string(rawBytes)
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return zero, false
	}
	return out, true
}

// millRunWorkflowName pins ExecutionService.runWorkflow's registered
// DBOS name -- see execution.WithWorkflowName's own doc comment for why
// this isn't left to derive from the bound method's runtime name.
const millRunWorkflowName = "mill.run-workflow"

// runInput is one durable run's DBOS workflow input -- WorkflowID is the
// composition.Workflow definition this run executes; the graph itself
// (Nodes/Edges/Attributes) travels alongside it so a run's own
// checkpointed history stays self-contained even if the definition is
// later edited or deleted, matching docs/adr/0004's "resume-without-
// re-execution" mapping.
type runInput struct {
	WorkflowID string
	Nodes      []composition.Node
	Edges      []composition.Edge
	Attributes []composition.AttributeDef
	Kind       RunKind
	// Values overrides the run's starting Attribute values (docs/adr/0008's
	// test-input form) -- keyed by AttributeDef.Key, same map[string]string
	// shape every other config value in this codebase already uses. Nil
	// (a "Run" click on a workflow with no declared Attributes, or any
	// caller that hasn't adopted the test-input form yet) behaves exactly
	// as before this field existed.
	Values map[string]string
	// Version records which definition snapshot this run executed
	// (docs/adr/0021) -- 0 means the draft head (a test run).
	Version int
	// Payload seeds the run's starting ExecContext.Payload
	// (composition.ExecuteOptions.InitialPayload) -- a headless trigger
	// fire's own event data (docs/SPEC.md §3.4's Trigger row), e.g. a
	// filesystem-watch trigger's changed file path. Empty for every run
	// started before this field existed (an already-persisted runInput
	// decodes it as "", identical to today's behavior) and for every
	// caller with nothing to offer (a manual/hotkey/schedule/clipboard-
	// watch fire, RunWorkflow's own zero-payload delegation below).
	Payload string
	// Stepped starts this run in debug "step mode" (docs/adr/0031 §5):
	// the guardrail gate parks before every node. False for every run
	// started before this field existed. Only RunWorkflowStepped sets
	// it true.
	Stepped bool
	// AtlasSourceCardID names the card whose write fired trigger-atlas-card
	// to start this run, "" otherwise (executionservice_atlascard.go).
	AtlasSourceCardID string
	// SecretsToken correlates this run to codeloopsvc's own in-memory
	// typed-secrets stash (goal 0240 S2, composition.ExecContext's own
	// field doc comment) -- an opaque, meaningless-without-the-stash
	// token, never the secret value itself, so persisting it as part of
	// this durably-checkpointed input is harmless. "" for every run
	// started before this field existed and every caller with no typed
	// secrets to correlate (RunWorkflow/RunWorkflowWithPayload/
	// RunWorkflowStepped's own zero-value delegation below).
	SecretsToken string
	// EnvironmentID names the Environment this run executes in (goal
	// 0306 S5) -- already resolved (the caller's override, else the
	// workflow's default) so a redrive or a resumed park replays the
	// stage the run actually started in, never whatever the default has
	// become since. "" for every run started before this field existed.
	EnvironmentID string
}

// RunStep is one node's recorded execution within a run, for the
// execution-visibility UI (docs/SPEC.md §3.2's "shows through the path
// ... where it stopped", researched from n8n). Status is
// "succeeded"/"failed"/"pending" -- pending means DBOS has no recorded
// step yet (the run never reached that node).
type RunStep struct {
	NodeID        string `json:"nodeID"`
	NodeTypeID    string `json:"nodeTypeID"`
	NodeTypeLabel string `json:"nodeTypeLabel"`
	Status        string `json:"status"`
	// Input/InputAttributes are this step's recorded INPUT (docs/adr/0031
	// item 3): the immediately-preceding EXECUTED step's own recorded
	// Payload/Attributes, or the run's own seeded starting values for
	// the first executed step. No omitempty on Input, matching Output
	// below (goal 0021 gap 3): omitempty on a genuinely-empty first
	// step dropped the JSON key entirely over MCP, indistinguishable
	// from a real mapping bug.
	Input           string         `json:"input"`
	InputAttributes map[string]any `json:"inputAttributes,omitempty"`
	Output          string         `json:"output"`
	// OutputAttributes is this step's Attributes bag AFTER it ran --
	// the DBOS checkpoint already stores the full ExecContext
	// (Payload+Attributes), but only Payload was ever decoded into
	// Output; Attributes was silently discarded until now.
	OutputAttributes map[string]any `json:"outputAttributes,omitempty"`
	Error            string         `json:"error"`
	// GuardrailEffect/GuardrailRule surface the step's recorded
	// guardrail verdict (docs/adr/0022) -- what actually decided at run
	// time, decoded from the checkpointed guardrail step, never a
	// re-evaluation against possibly-changed rules. Empty when the
	// effect-class default allowed without any rule involved.
	GuardrailEffect string `json:"guardrailEffect,omitempty"`
	GuardrailRule   string `json:"guardrailRule,omitempty"`
	// GuardrailSource mirrors the recorded verdict's Source
	// (guardrail.SourceDebug for a breakpoint/step-mode park, "" for
	// policy) -- the Runs tab's own distinct debug badge (docs/adr/0031
	// item 2), never conflated with the policy shield.
	GuardrailSource string `json:"guardrailSource,omitempty"`
	// Waits lists every park this step went through before it ran --
	// today only a vault wait (executionservice_vaultwait.go's RunWait).
	Waits []RunWait `json:"waits,omitempty"`
	// CompletedAt is when THIS step's own last recorded attempt finished
	// (DBOS's own StepInfo.CompletedAt, goal 0350 S3) -- zero for a step
	// that hasn't executed yet ("pending"), so a run's receipt can
	// answer "when did this step finish" per step, not only for the run
	// as a whole (RunSummary.CompletedAt).
	CompletedAt time.Time `json:"completedAt,omitzero"`
}

// RunSummary is one run's headline state -- the row shape for a
// run-history list.
type RunSummary struct {
	RunID         string    `json:"runID"`
	WorkflowID    string    `json:"workflowID"`
	WorkflowLabel string    `json:"workflowLabel"`
	Status        string    `json:"status"`
	Kind          RunKind   `json:"kind"`
	Output        string    `json:"output"`
	StartedAt     time.Time `json:"startedAt"`
	CompletedAt   time.Time `json:"completedAt"`
	Error         string    `json:"error"`
	// Version is which definition snapshot executed (docs/adr/0021) --
	// 0 means the draft head (a test run).
	Version int `json:"version"`
	// Pending is the run's live awaiting-approval state, if any
	// (docs/adr/0022) -- non-nil only while a guardrail ask is parked.
	Pending *PendingApproval `json:"pending,omitempty"`
	// Resolution is a completed run's approval outcome ("approved"/
	// "denied"/"timed out"), empty when the run never parked -- the
	// Review queue's recently-resolved section (goal 0002).
	Resolution string `json:"resolution,omitempty"`
	// Interrupted marks a run that was parked on an approval when Mill
	// relaunched under a different WorkflowCodeVersion and so could not
	// be recovered -- ReconcileInterrupted cancelled it at startup
	// (executionservice_reconcile.go). Distinct from a person stopping
	// a run: nobody answered, the answer became unanswerable.
	Interrupted bool `json:"interrupted,omitempty"`
	// Values are the attribute values this run was invoked with
	// (runInput.Values -- a test form's input, or a parent's resolved
	// child bindings). The data behind Activity's per-attribute columns
	// and attribute search (docs/SPEC.md §3.2's analytics pattern).
	Values map[string]string `json:"values"`
	// EnvironmentID/EnvironmentLabel name the Environment this run
	// executed in (goal 0306 S5) -- the label is what Activity shows,
	// the id is what a redrive or a filter matches on. Both empty for a
	// run that selected no environment.
	EnvironmentID    string `json:"environmentID,omitempty"`
	EnvironmentLabel string `json:"environmentLabel,omitempty"`
}

// RunDetail is a RunSummary plus its full per-node step breakdown.
type RunDetail struct {
	RunSummary
	Steps []RunStep `json:"steps"`
}

// ExecutionService is the Wails-facing layer over
// internal/adapters/execution's durable-workflow runtime -- it holds no
// domain logic of its own (that's composition.ExecuteWorkflowWithStepRunner),
// only the DBOS wiring and the Mill-specific run/step shapes a stateless
// adapter package can't own, mirroring CompositionService's own split.
// See docs/adr/0004's Update for the full workflow/step mapping this
// implements.
type ExecutionService struct {
	ctx   execution.Context
	comp  *compositionsvc.CompositionService
	guard *guardrailsvc.GuardrailService
	// cancelState (executionservice_cancel.go) holds the live-process
	// registry docs/adr/0026's cancellation design needs -- embedded
	// rather than a named field since callers never reach through it
	// directly, only via CancelRun/registerProcess.
	cancelState
	// parkedRuns is the live-park registry: runID -> nodeID for every
	// run parked on Recv inside THIS process. A run whose row says
	// PENDING but which is absent here has no listener, so a Send would
	// be accepted by the database and never received -- ResolveApproval
	// answers instead of silently doing nothing
	// (executionservice_guardrail.go).
	parkedRuns sync.Map
	// vaultWaits is the subset of parkedRuns waiting on the vault
	// (executionservice_vaultwait.go): runID -> vaultWait, so an unlock
	// can resume exactly those, oldest first, and nothing else.
	vaultWaits sync.Map
	// vaultLocked answers whether the vault is locked right now -- wired
	// from the secret service via SetVaultLockedLookup; nil means never.
	vaultLocked func() bool
	// minutesSavedLookup resolves a workflow's current "minutes saved
	// per run" estimate -- SettingsService's own persisted preference
	// (docs/SPEC.md §3.7), wired in from main.go via
	// SetMinutesSavedLookup, same injected-function-seam shape as
	// composition.SetHTTPRequestLookup (.claude/rules/backend.md: data
	// another layer owns, not a direct cross-service import). Nil until
	// wired (e.g. a standalone Go test constructing ExecutionService
	// directly) -- minutesSavedFor (executionservice_home.go) falls back
	// to defaultMinutesSavedPerRun in that case.
	minutesSavedLookup func(workflowID string) int
	// systemEventSink is the injected dispatch seam for docs/adr/0035's
	// trigger-system-event family -- see executionservice_systemevent.go.
	// Production installs it between preparation and durable recovery;
	// nil in standalone tests means no system-event consumer.
	systemEventSink func(SystemEvent)
	// runCompletionSink is goal 0061 slice C's run-completion seam
	// (ADR-0038 decision 4) -- called from runWorkflow for EVERY run's
	// completion, success or failure, so atlassvc's UpdateNow can stamp
	// a card's LastSyncedAt whenever its "Update now" run actually
	// succeeds, however long that takes (including a run that parked
	// for guardrail approval and resolved long after the call that
	// started it returned). Wired from main.go via
	// SetRunCompletionSink; nil in every standalone test, same as
	// systemEventSink above. Production installs it before recovery too.
	runCompletionSink func(runID string, succeeded bool)
	// version is the app version string a run receipt's Build field
	// stamps (executionservice_receipt.go) -- set via SetVersion once
	// main.go's millVersion const is available; empty in every
	// standalone test that builds ExecutionService directly, same as
	// minutesSavedLookup/systemEventSink above.
	// environmentLabelLookup resolves an Environment id to its label
	// for a run summary/receipt (goal 0306 S5) -- ConfigureService owns
	// that data, and this service must not import it directly
	// (.claude/rules/backend.md). Nil until wired; an unwired lookup
	// simply leaves the label empty, never an error.
	environmentLabelLookup func(environmentID string) string
	version                string
	// appVersion is the durable runtime's own application version --
	// WorkflowCodeVersion in production, a test-supplied value via
	// NewExecutionServiceWithVersion. Every "was this row written by
	// this code?" comparison reads this, never the const directly, so a
	// test relaunching one database under two versions gets honest
	// answers from both services.
	appVersion string
	// responders holds the live webhook responder for a run the
	// ingress started (goal 0373) -- see
	// executionservice_webhookresponder.go's own doc comment.
	responders sync.Map
	// runStartMu serializes provider-dependent preflight and the moment a
	// parent, child, or redrive is handed to the durable runtime. Provider
	// impact scans and mutations use the same lock, so a run starts against
	// either the configuration before a mutation or the configuration after it.
	// The lock never spans the run's later execution or GetResult wait. Goal 0395's
	// own -race reproduction (against DBOS v1.3.0) is a launch racing a
	// concurrent Shutdown() on the runtime's shared per-context
	// bookkeeping (context.AfterFunc inside its executeWorkflow), fixed
	// by triggersvc's fireWG/Drain handshake that keeps a fire from
	// still being inside a launch call when Shutdown starts -- this
	// mutex began as the extension of that same reasoning to two launches
	// racing each other on the same durable context; provider mutation safety
	// now gives this ordering a directly tested second purpose.
	runStartMu         sync.Mutex
	aiProviderMutation aiProviderMutationState
	aiProviderSamples  aiProviderSampleRuntime
}

// runWorkflow is the one DBOS-registered durable workflow function --
// every Mill workflow run, regardless of which composition.Workflow
// definition it executes, goes through this single registration
// (RunWorkflow's input carries which graph to run). Its body wraps each
// node's execution in a checkpointed DBOS step, keyed by the node's own
// ID (execution.WithStepName), via composition's injected StepRunner
// seam -- composition itself never imports DBOS (domain purity).
func (e *ExecutionService) runWorkflow(ctx execution.Context, in runInput) (string, error) {
	runID, err := ctx.GetWorkflowID()
	if err != nil {
		return "", fmt.Errorf("read workflow ID before provider resolution: %w", err)
	}
	leaveProviderBody, err := enterAIProviderRunBody(e, runID, in)
	if err != nil {
		return "", err
	}
	defer leaveProviderBody()
	stepRunner := func(stepID string, fn func() (composition.ExecContext, error)) (composition.ExecContext, error) {
		return e.runStep(ctx, in, stepID, fn)
	}
	responder, hasResponder := e.loadResponder(runID)
	if hasResponder {
		defer e.responders.Delete(runID)
	}
	output, err := composition.ExecuteWorkflowWithStepRunner(in.Nodes, in.Edges, in.Attributes, stepRunner,
		composition.ExecuteOptions{
			AttrValues: in.Values, RunContext: ctx, InitialPayload: in.Payload,
			EnvironmentID: in.EnvironmentID,
			// WorkflowID was never threaded through here before this
			// change -- a real, previously-latent bug found while
			// building breakpoints (docs/adr/0031): every workflow- and
			// node-instance-scoped guardrail rule (ADR-0019's third
			// scope, which a breakpoint IS) evaluated against an always-
			// empty WorkflowID and so could never actually match at run
			// time, even though guardrailservice.go's dry-run tester
			// (which does pass workflowID) reported the rule as live.
			// Fixed here, load-bearing for this feature, not a
			// speculative unrelated cleanup.
			WorkflowID:   in.WorkflowID,
			Stepped:      in.Stepped,
			SecretsToken: in.SecretsToken,
			Responder:    responder,
		})

	// run-completed/run-failed (docs/adr/0035 item 4): emitted HERE, not
	// at an observation call site (RunWorkflow's own GetResult, or
	// TriggerService.fire's summary read), because this DBOS-registered
	// function is the one place that runs to completion for EVERY run
	// regardless of how it started or whether anything is still awaiting
	// its result -- a run that parked and later resumed asynchronously
	// (a human approving hours later) finishes here too, long after
	// RunWorkflowStart's own caller already returned. One emission point
	// covers both "the GetResult completion path AND triggerservice's
	// fire completion" the goal names, since both are downstream of this
	// same function. run-cancelled is deliberately NOT decided here --
	// CancelRun (executionservice_cancel.go) emits it directly, since
	// that's the synchronous, user-initiated stop path; skip here (via
	// the CancelledByUserMessage marker, the same one
	// executionservice_getrun.go already uses to tell "cancelled" apart
	// from "failed" at the step level) so a cancelled run isn't ALSO
	// reported as failed.
	switch {
	case err == nil:
		e.emitSystemEvent(SystemEventRunCompleted, runID, "")
		if e.runCompletionSink != nil {
			e.runCompletionSink(runID, true)
		}
	case strings.Contains(err.Error(), composition.CancelledByUserMessage):
		// CancelRun already emitted run-cancelled for this runID.
	default:
		e.emitSystemEvent(SystemEventRunFailed, runID, "")
		if e.runCompletionSink != nil {
			e.runCompletionSink(runID, false)
		}
	}
	// Live-sync counterpart of runWorkflowStart's start emit, placed
	// here for the same reason as the system events above: every run
	// finishes through this function (including a parked run resumed
	// hours later), so open run lists refresh to the final status
	// without a reload. Unconditional -- cancelled and failed rows go
	// stale exactly like successful ones.
	dataevent.Emit("run", runID)
	return output, err
}

func (e *ExecutionService) runWorkflowStart(workflowID string, kind RunKind, opts RunOptions) (RunSummary, error) {
	wf, ok := e.findWorkflow(workflowID)
	if !ok {
		return RunSummary{}, fmt.Errorf("unknown workflow: %s", workflowID)
	}
	environmentID := opts.environmentFor(wf)

	// ADR-0021: a test or MCP run executes the draft head (the
	// pre-publish check, RunKind.runsDraft); a triggered run executes
	// the published snapshot and is rejected on a disabled or
	// never-published workflow.
	nodes, edges, attrs, version, err := composition.ResolveRunnable(wf, kind.runsDraft(), 0)
	if err != nil {
		return RunSummary{}, err
	}

	e.runStartMu.Lock()
	if err := preflightRefusal(nodes, edges, attrs, environmentID); err != nil {
		e.runStartMu.Unlock()
		return RunSummary{}, err
	}
	returnBeforeResult := opts.Stepped || e.mayRequireApproval(wf.ID, nodes) || e.mayWaitForVault(wf.ID, nodes)
	runID := uuid.NewString()
	sampleReserved := e.reserveAIProviderSampleRun(wf, nodes, edges, attrs, kind, opts, runID)
	e.storeResponder(runID, opts.Responder)
	handle, err := execution.RunWorkflow(e.ctx, e.runWorkflow, runInput{
		WorkflowID:        wf.ID,
		Nodes:             nodes,
		Edges:             edges,
		Attributes:        attrs,
		Kind:              kind,
		Values:            opts.Values,
		Version:           version,
		Payload:           opts.Payload,
		Stepped:           opts.Stepped,
		AtlasSourceCardID: opts.AtlasSourceCardID,
		SecretsToken:      opts.SecretsToken,
		EnvironmentID:     environmentID,
	}, execution.WithWorkflowID(runID))
	sampleActivated := e.finishAIProviderSampleGenesis(runID, sampleReserved, err)
	e.runStartMu.Unlock()
	if err != nil {
		e.responders.Delete(runID)
		return RunSummary{}, fmt.Errorf("start run: %w", err)
	}
	if sampleActivated {
		e.observeAIProviderSampleRun(runID)
	}

	// Live-sync (goal 0017, docs/adr/0025): announce the new run so an
	// already-open Runs panel shows it without a remount -- the panel
	// listens for mill-data-changed{entity:"run"} and stays mounted
	// across section-tab switches, so a missing start emit leaves it
	// stale until a full reload. Completion emits from runWorkflow
	// itself (the one point every run finishes through).
	dataevent.Emit("run", runID)

	// Blocking -- matches the plain-Run UX this replaces (docs/adr/0008):
	// Mill's node executions (clipboard/HTTP/MCP calls) are sub-second to
	// a few seconds, and every run's full step history is durably
	// queryable afterward regardless (ListRuns/GetRun), so a
	// live-streaming progress view isn't required for the "see what
	// happened" half of execution visibility this exists for.
	// EXCEPT when the graph could park awaiting a guardrail approval
	// (docs/adr/0022), this is a stepped run (guaranteed to park at
	// its very first node, docs/adr/0031), or a step will read a secret
	// while the vault is locked (executionservice_vaultwait.go): then
	// return immediately with the run ID, so a Run click never hangs on
	// a human decision or an unlock -- the pending state surfaces via
	// RunSummary.Pending instead.
	if returnBeforeResult {
		return e.summaryFor(handle.GetWorkflowID())
	}
	if _, err := handle.GetResult(); err != nil {
		// Not returned as a Go error -- a failed *run* is a normal,
		// inspectable/redrivable outcome (that's the whole point of
		// this endpoint), not a call failure. The summary's own Status/
		// Error fields carry it.
		_ = err
	}

	return e.summaryFor(handle.GetWorkflowID())
}

func (e *ExecutionService) findWorkflow(workflowID string) (composition.Workflow, bool) {
	for _, wf := range e.comp.Workflows() {
		if wf.ID == workflowID {
			return wf, true
		}
	}
	return composition.Workflow{}, false
}
