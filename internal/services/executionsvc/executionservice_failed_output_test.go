package executionsvc

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alicoding/mill/internal/domain/composition"
	"github.com/alicoding/mill/internal/domain/guardrail"
	"github.com/alicoding/mill/internal/services/compositionsvc"
	"github.com/alicoding/mill/internal/services/guardrailsvc"
	"github.com/alicoding/mill/internal/services/servicetest"
)

func newFailedOutputHarness(t *testing.T, shellEffect guardrail.Effect) (*ExecutionService, *compositionsvc.CompositionService) {
	t.Helper()
	store := servicetest.NewFakeStore()
	comp := compositionsvc.NewCompositionService(store)
	guard := guardrailsvc.NewGuardrailService(store, comp)
	if _, err := guard.CreateRule(guardrail.Rule{
		Label: "Failed output shell fixture", Effect: shellEffect, NodeTypeID: "process-shell-command",
	}); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}
	exec, err := NewExecutionService("sqlite:"+filepath.Join(t.TempDir(), "exec.db"), comp, guard)
	if err != nil {
		t.Fatalf("NewExecutionService: %v", err)
	}
	t.Cleanup(func() { _ = exec.Shutdown(2 * time.Second) })
	return exec, comp
}

func awaitRunTerminal(t *testing.T, exec *ExecutionService, runID string) RunSummary {
	t.Helper()
	return waitFor(t, "run to reach a terminal state", 15*time.Second, func() (RunSummary, bool) {
		summary, err := exec.summaryFor(runID)
		if err != nil {
			return RunSummary{}, false
		}
		terminal := summary.Status == "SUCCESS" || summary.Status == "ERROR" || summary.Status == "MAX_RECOVERY_ATTEMPTS_EXCEEDED"
		return summary, terminal
	})
}

func TestGetRun_FailedShellRetainsCheckpointedOutputWithoutRunningDownstream(t *testing.T) {
	exec, comp := newFailedOutputHarness(t, guardrail.EffectAllow)
	fixtureDir := t.TempDir()
	scriptPath := filepath.Join(fixtureDir, "command.sh")
	counterPath := filepath.Join(fixtureDir, "runs.txt")
	downstreamPath := filepath.Join(fixtureDir, "downstream.txt")
	const marker = "failed-output-fixture-marker"
	script := fmt.Sprintf("printf 'once\\n' >> %q; printf '%s\\n'; exit 7", counterPath, marker)
	if err := os.WriteFile(scriptPath, []byte(script), 0o600); err != nil {
		t.Fatalf("WriteFile command fixture: %v", err)
	}

	wf, err := comp.CreateWorkflow("failed output fixture", "", []composition.Node{
		{ID: "trigger", NodeTypeID: "trigger-manual"},
		{ID: "read", NodeTypeID: "capture-file", Config: map[string]string{"source": "payload"}},
		{ID: "shell", NodeTypeID: "process-shell-command"},
		{ID: "apply", NodeTypeID: "apply-file-write", Config: map[string]string{
			"path": downstreamPath, "mode": "overwrite", "createDirs": "true",
		}},
	}, []composition.Edge{
		{ID: "e1", Source: "trigger", Target: "read"},
		{ID: "e2", Source: "read", Target: "shell"},
		{ID: "e3", Source: "shell", Target: "apply"},
	})
	if err != nil {
		t.Fatalf("CreateWorkflow: %v", err)
	}

	started, err := exec.RunWorkflowWithPayload(wf.ID, RunKindTest, nil, scriptPath)
	if err != nil {
		t.Fatalf("RunWorkflowWithPayload: %v", err)
	}
	final := awaitRunTerminal(t, exec, started.RunID)
	if final.Status != "ERROR" {
		t.Fatalf("run status = %q, want ERROR (error: %s)", final.Status, final.Error)
	}

	detail, err := exec.GetRun(started.RunID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if detail.Status != "ERROR" {
		t.Errorf("detail status = %q, want ERROR", detail.Status)
	}
	if !strings.Contains(detail.Output, marker) {
		t.Fatalf("detail output = %q, want failing shell marker %q", detail.Output, marker)
	}

	var readStep, shellStep, applyStep *RunStep
	for i := range detail.Steps {
		switch detail.Steps[i].NodeID {
		case "read":
			readStep = &detail.Steps[i]
		case "shell":
			shellStep = &detail.Steps[i]
		case "apply":
			applyStep = &detail.Steps[i]
		}
	}
	if readStep == nil || shellStep == nil || applyStep == nil {
		t.Fatalf("GetRun steps = %+v, want read, shell, and apply", detail.Steps)
	}
	if readStep.Status != "succeeded" {
		t.Errorf("read status = %q, want succeeded", readStep.Status)
	}
	if shellStep.Status != "failed" || shellStep.Error == "" {
		t.Errorf("shell step = %+v, want failed with its error retained", *shellStep)
	}
	if shellStep.Output != detail.Output {
		t.Errorf("detail output = %q, want failing checkpoint output %q", detail.Output, shellStep.Output)
	}
	if detail.Output == readStep.Output {
		t.Errorf("detail output selected the earlier successful step %q", readStep.Output)
	}
	if applyStep.Status != "pending" {
		t.Errorf("downstream apply status = %q, want pending because execution stopped at the failed shell", applyStep.Status)
	}
	if _, err := os.Stat(downstreamPath); !os.IsNotExist(err) {
		t.Fatalf("downstream output file stat error = %v, want file not to exist", err)
	}

	second, err := exec.GetRun(started.RunID)
	if err != nil {
		t.Fatalf("second GetRun: %v", err)
	}
	if second.Output != detail.Output {
		t.Errorf("second GetRun output = %q, want durable output %q", second.Output, detail.Output)
	}
	counter, err := os.ReadFile(counterPath) //nolint:gosec // Test-owned path under t.TempDir.
	if err != nil {
		t.Fatalf("ReadFile counter: %v", err)
	}
	if got := strings.Count(string(counter), "once\n"); got != 1 {
		t.Errorf("shell execution count = %d, want 1 after two GetRun calls", got)
	}
}

func TestGetRun_DeniedStepDoesNotSubstituteGuardrailOutput(t *testing.T) {
	exec, comp := newFailedOutputHarness(t, guardrail.EffectDeny)
	wf, err := comp.CreateWorkflow("denied output fixture", "", []composition.Node{
		{ID: "trigger", NodeTypeID: "trigger-manual"},
		{ID: "shell", NodeTypeID: "process-shell-command"},
	}, []composition.Edge{{ID: "e1", Source: "trigger", Target: "shell"}})
	if err != nil {
		t.Fatalf("CreateWorkflow: %v", err)
	}

	started, err := exec.RunWorkflowWithPayload(wf.ID, RunKindTest, nil, "printf should-not-run")
	if err != nil {
		t.Fatalf("RunWorkflowWithPayload: %v", err)
	}
	final := awaitRunTerminal(t, exec, started.RunID)
	if final.Status != "ERROR" {
		t.Fatalf("run status = %q, want ERROR (error: %s)", final.Status, final.Error)
	}
	detail, err := exec.GetRun(started.RunID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if detail.Output != "" {
		t.Errorf("detail output = %q, want empty because the denied node has no checkpointed output", detail.Output)
	}
	if len(detail.Steps) != 1 || detail.Steps[0].Status != "denied" {
		t.Fatalf("steps = %+v, want the shell step to remain denied", detail.Steps)
	}
}
