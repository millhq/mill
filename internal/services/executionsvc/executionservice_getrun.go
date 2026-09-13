package executionsvc

import (
	"fmt"
	"sort"
	"strings"

	"github.com/alicoding/mill/internal/adapters/execution"
	"github.com/alicoding/mill/internal/domain/composition"
	"github.com/alicoding/mill/internal/domain/guardrail"
	"github.com/alicoding/mill/internal/domain/secret"
)

// GetRun -- split out of executionservice.go once that file crossed the
// 500-line limit (CLAUDE.md/§1.3), the same "split along a real seam"
// discipline this package's other split files already established. This
// is a genuinely separable concern: decoding one run's per-step
// breakdown, not starting/listing/redriving a run.

// GetRun returns one run's full per-node step breakdown, joined against
// its workflow definition's current Nodes (by ID) for display labels --
// falls back to a bare step list (no labels) if the definition was
// since edited/deleted, rather than failing the whole call over
// missing display metadata.
//
// Steps that DID execute are returned in DBOS's own recorded execution
// order (StepID, sequential), not the graph DEFINITION's node order
// (docs/adr/0031 item 3): walking wf.Nodes was a real, previously-
// unfixed bug for any branching workflow -- a Decision node routing to
// the graph's SECOND-declared arm before its first would still list
// them in declaration order, misreporting what actually ran first.
// Anything the run hasn't reached yet (or, for a completed run, never
// reached down an untaken branch) is appended after, still "pending" --
// same visibility as before this fix, just no longer interleaved with
// the real executed sequence.
func (e *ExecutionService) GetRun(runID string) (RunDetail, error) {
	summary, err := e.summaryFor(runID)
	if err != nil {
		return RunDetail{}, err
	}

	steps, err := execution.GetWorkflowSteps(e.ctx, runID)
	if err != nil {
		return RunDetail{}, fmt.Errorf("get run steps: %w", err)
	}

	// The run's OWN seeded starting state (docs/adr/0004: a run's graph
	// and starting values travel in its own checkpointed Input, self-
	// contained regardless of any later definition edit) -- the first
	// executed step's recorded INPUT (docs/adr/0031 item 3: "the run's
	// InitialPayload/attribute seed for the first step").
	var initialPayload string
	var initialAttrs map[string]any
	if statuses, sErr := execution.ListWorkflows(e.ctx, execution.WithFilterWorkflowIDs(runID)); sErr == nil && len(statuses) > 0 {
		if in, ok := decodeAny[runInput](statuses[0].Input); ok {
			initialPayload = in.Payload
			initialAttrs = composition.AttributesEnv(in.Attributes, in.Values)
		}
	}

	wf, haveWF := e.findWorkflow(summary.WorkflowID)
	byID := make(map[string]composition.Node, len(wf.Nodes))
	if haveWF {
		for _, n := range wf.Nodes {
			byID[n.ID] = n
		}
	}
	typeLabels := make(map[string]string)
	for _, nt := range composition.NodeTypes() {
		typeLabels[nt.ID] = nt.Label
	}

	sortedSteps := make([]execution.StepInfo, len(steps))
	copy(sortedSteps, steps)
	sort.Slice(sortedSteps, func(i, j int) bool { return sortedSteps[i].StepID < sortedSteps[j].StepID })

	// A node re-run after a vault wait records one attempt per try
	// (executionservice_vaultwait.go): the LAST attempt is the step's
	// outcome, every earlier vault-locked attempt is one wait, timed
	// from that attempt's end to the next attempt's start.
	byNodeID := make(map[string]execution.StepInfo, len(sortedSteps))
	waitsByNodeID := make(map[string][]RunWait)
	for _, s := range sortedSteps {
		if prev, ok := byNodeID[s.StepName]; ok && secret.IsVaultLocked(prev.Error) {
			waitsByNodeID[s.StepName] = append(waitsByNodeID[s.StepName], RunWait{Reason: ParkReasonVaultLocked, ParkedAt: prev.CompletedAt, ResumedAt: s.StartedAt})
		}
		byNodeID[s.StepName] = s
	}

	// Real executed node order -- "guardrail:<nodeID>" pseudo-steps
	// excluded here (joined back in per-node below, same as before);
	// Trigger/Decision nodes never get their own checkpointed step
	// (execute.go skips run() for both), so nothing to filter for those.
	// DBOS's own system operations (Send/Recv/SetEvent/Sleep -- e.g. an
	// approval park records DBOS.setEvent + DBOS.recv + DBOS.sleep) are
	// checkpointed as steps too, under the library's uniform "DBOS."
	// name prefix -- excluded, or each would render as a blank,
	// label-less row in the run detail (an approved
	// guardrail run would show three bare checkmarks above its real steps).
	seen := make(map[string]bool, len(sortedSteps))
	var order []composition.Node
	for _, s := range sortedSteps {
		if strings.HasPrefix(s.StepName, "guardrail:") || strings.HasPrefix(s.StepName, "DBOS.") || seen[s.StepName] {
			continue
		}
		seen[s.StepName] = true
		if n, ok := byID[s.StepName]; ok {
			order = append(order, n)
		} else {
			order = append(order, composition.Node{ID: s.StepName})
		}
	}
	// Not-yet-(or-never)-executed nodes, in definition order -- a
	// still-in-flight run's future steps, or a completed run's untaken
	// branch.
	if haveWF {
		for _, n := range wf.Nodes {
			if n.Kind == composition.KindTrigger || n.Kind == composition.KindDecision || seen[n.ID] {
				continue
			}
			order = append(order, n)
		}
	}

	var view []RunStep
	prevPayload, prevAttrs := initialPayload, initialAttrs
	var failedOutput string
	haveFailedOutput := false
	for _, n := range order {
		rs := RunStep{
			NodeID: n.ID, NodeTypeID: n.NodeTypeID, NodeTypeLabel: typeLabels[n.NodeTypeID], Status: "pending",
			Input: prevPayload, InputAttributes: prevAttrs,
		}
		rs.Waits = waitsByNodeID[n.ID]
		if s, ok := byNodeID[n.ID]; ok {
			if secret.IsVaultLocked(s.Error) {
				// The latest attempt ended at the locked vault and no
				// later attempt exists yet: the run is waiting there.
				rs.Waits = append(rs.Waits, RunWait{Reason: ParkReasonVaultLocked, ParkedAt: s.CompletedAt})
			}
			rs.CompletedAt = s.CompletedAt
			out, outputDecoded := decodeAny[composition.ExecContext](s.Output)
			if outputDecoded {
				rs.Output = out.Payload
				rs.OutputAttributes = out.Attributes
			}
			if s.Error != nil {
				rs.Status = "failed"
				rs.Error = s.Error.Error()
				if outputDecoded && !haveFailedOutput {
					failedOutput = out.Payload
					haveFailedOutput = true
				}
				// A code-execution step killed via CancelRun records a
				// distinct status (docs/adr/0026: "cancelled != failed !=
				// interrupted") -- matched by message, not error identity,
				// since a checkpointed step's error crosses a DBOS
				// JSON round trip (decodeAny's own doc comment) that
				// only preserves Error(), never Go type/errors.Is.
				if strings.Contains(rs.Error, composition.CancelledByUserMessage) {
					rs.Status = "cancelled"
				}
			} else {
				rs.Status = "succeeded"
				if outputDecoded {
					// This step's OUTPUT becomes the next EXECUTED step's
					// INPUT -- only advanced on a real success, so
					// anything appended after it in "pending" still shows
					// the last real state as its input rather than blank.
					prevPayload, prevAttrs = out.Payload, out.Attributes
				}
			}
		}
		// Join the step's recorded guardrail verdict (docs/adr/0022) --
		// checkpointed under its own "guardrail:<nodeID>" step name.
		if g, ok := byNodeID["guardrail:"+n.ID]; ok && g.Error == nil {
			if v, ok := decodeAny[guardrail.Verdict](g.Output); ok {
				rs.GuardrailEffect = string(v.Effect)
				rs.GuardrailRule = v.RuleLabel
				rs.GuardrailSource = v.Source
				if v.Effect == guardrail.EffectDeny && rs.Status == "pending" {
					rs.Status = "denied"
				}
			}
		}
		if summary.Pending != nil && summary.Pending.NodeID == n.ID {
			rs.Status = "awaiting-approval"
		}
		view = append(view, rs)
	}

	if summary.Status == "ERROR" && summary.Output == "" && haveFailedOutput {
		summary.Output = failedOutput
	}

	return RunDetail{RunSummary: summary, Steps: view}, nil
}

// ListRuns returns recent runs across every workflow, most recent
// first -- the data behind Activity's cross-workflow "did anything run"
// feed and any other surface needing every run regardless of which
// workflow it belongs to.
func (e *ExecutionService) ListRuns() ([]RunSummary, error) {
	return e.listRuns(nil)
}

// ListRunsForWorkflow returns recent runs for one workflow only, most
// recent first -- the data behind a workflow's own Runs tab
// (docs/SPEC.md §7's Update: durable-run visibility moved from a
// standalone page into the workflow it belongs to, per real precedent
// -- n8n/Retool/Airflow all scope this to the individual workflow's own
// page, never a global page reached via a workflow picker). DBOS has no
// native filter on runInput.WorkflowID (an arbitrary field inside the
// generically-serialized Input, not something ListWorkflows' own
// filters -- WithFilterWorkflowIDs et al. -- can query against), so this
// filters post-decode the same way summaryFromStatus already decodes
// runInput for every other field.
func (e *ExecutionService) ListRunsForWorkflow(workflowID string) ([]RunSummary, error) {
	return e.listRuns(&workflowID)
}

func (e *ExecutionService) listRuns(filterWorkflowID *string) ([]RunSummary, error) {
	statuses, err := execution.ListWorkflows(e.ctx,
		execution.WithFilterName(millRunWorkflowName),
		execution.WithFilterSortDesc(),
		execution.WithFilterLimit(50),
	)
	if err != nil {
		return nil, fmt.Errorf("list runs: %w", err)
	}

	summaries := make([]RunSummary, 0, len(statuses))
	for _, st := range statuses {
		summary := e.summaryFromStatus(st)
		if filterWorkflowID != nil && summary.WorkflowID != *filterWorkflowID {
			continue
		}
		summaries = append(summaries, summary)
	}
	return summaries, nil
}
