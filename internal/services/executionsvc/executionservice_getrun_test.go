package executionsvc

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alicoding/mill/internal/domain/composition"
	"github.com/alicoding/mill/internal/services/compositionsvc"
	"github.com/alicoding/mill/internal/services/guardrailsvc"
	"github.com/alicoding/mill/internal/services/servicetest"
)

// TestGetRun_MultiStepInput_PopulatedAndChained is goal 0021 gap 3's
// repro: a live dogfood pass of get_run over MCP saw inputAttributes
// present but no "input" key at all on a fresh run, raising the
// question of whether Input's mapping was genuinely broken (silently
// empty/wrong) or just the expected omitempty-drops-an-empty-string
// case (a run with no seeded payload legitimately has nothing for its
// first step's Input). This proves the mapping itself on a REAL
// multi-step run with a real, non-empty payload: capture-file's INPUT
// must be the run's own seeded payload (the file path), and the
// downstream process-inject-text step's INPUT must be exactly
// capture-file's OUTPUT (the file's contents) -- not empty, not stale,
// not the wrong step's data.
func TestGetRun_MultiStepInput_PopulatedAndChained(t *testing.T) {
	store := servicetest.NewFakeStore()
	comp := compositionsvc.NewCompositionService(store)
	guard := guardrailsvc.NewGuardrailService(store, comp)
	dbPath := filepath.Join(t.TempDir(), "exec.db")
	exec, err := NewExecutionService("sqlite:"+dbPath, comp, guard)
	if err != nil {
		t.Fatalf("NewExecutionService: %v", err)
	}
	t.Cleanup(func() { _ = exec.Shutdown(2 * time.Second) })

	wf, err := comp.CreateWorkflow("multi-step input test", "", []composition.Node{
		{ID: "t", NodeTypeID: "trigger-manual", Kind: composition.KindTrigger, Position: composition.Position{X: 0, Y: 0}},
		{ID: "c", NodeTypeID: "capture-file", Kind: composition.KindCapture,
			Config:   map[string]string{"source": "payload"},
			Position: composition.Position{X: 0, Y: 100}},
		{ID: "p", NodeTypeID: "process-inject-text", Kind: composition.KindProcess,
			Config:   map[string]string{"text": "TAG", "placement": "append"},
			Position: composition.Position{X: 0, Y: 200}},
	}, []composition.Edge{
		{ID: "e1", Source: "t", Target: "c"},
		{ID: "e2", Source: "c", Target: "p"},
	})
	if err != nil {
		t.Fatalf("CreateWorkflow: %v", err)
	}

	filePath := filepath.Join(t.TempDir(), "input.txt")
	const fileContent = "real file contents"
	if err := os.WriteFile(filePath, []byte(fileContent), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	summary, err := exec.RunWorkflowWithPayload(wf.ID, RunKindTest, nil, filePath)
	if err != nil {
		t.Fatalf("RunWorkflowWithPayload: %v", err)
	}
	final := waitFor(t, "run to succeed", 10*time.Second, func() (RunSummary, bool) {
		s, err := exec.summaryFor(summary.RunID)
		if err != nil || (s.Status != "SUCCESS" && s.Status != "ERROR") {
			return RunSummary{}, false
		}
		return s, true
	})
	if final.Status != "SUCCESS" {
		t.Fatalf("run status = %q, want SUCCESS (error: %s)", final.Status, final.Error)
	}

	detail, err := exec.GetRun(summary.RunID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}

	var captureStep, injectStep *RunStep
	for i := range detail.Steps {
		switch detail.Steps[i].NodeID {
		case "c":
			captureStep = &detail.Steps[i]
		case "p":
			injectStep = &detail.Steps[i]
		}
	}
	if captureStep == nil || injectStep == nil {
		t.Fatalf("GetRun did not report both steps: %+v", detail.Steps)
	}

	if captureStep.Input != filePath {
		t.Errorf("capture-file step Input = %q, want the run's seeded payload (the file path) %q", captureStep.Input, filePath)
	}
	if captureStep.Output != fileContent {
		t.Errorf("capture-file step Output = %q, want the file's contents %q", captureStep.Output, fileContent)
	}
	if injectStep.Input != fileContent {
		t.Errorf("process-inject-text step Input = %q, want capture-file's OUTPUT (%q) -- the chained input mapping", injectStep.Input, fileContent)
	}
	want := fileContent + "\n\nTAG"
	if injectStep.Output != want {
		t.Errorf("process-inject-text step Output = %q, want %q", injectStep.Output, want)
	}
	if detail.Output != want {
		t.Errorf("successful detail Output = %q, want unchanged terminal output %q", detail.Output, want)
	}
}

// TestGetRun_CompletedAt_SetOnExecutedStepsOnly is goal 0350 S3's own
// repro: a receipt built mid-run has to answer "when did THIS step
// finish" per step, which needs RunStep.CompletedAt populated for
// every step that actually ran and left zero for one that hasn't.
func TestGetRun_CompletedAt_SetOnExecutedStepsOnly(t *testing.T) {
	store := servicetest.NewFakeStore()
	comp := compositionsvc.NewCompositionService(store)
	guard := guardrailsvc.NewGuardrailService(store, comp)
	dbPath := filepath.Join(t.TempDir(), "exec.db")
	exec, err := NewExecutionService("sqlite:"+dbPath, comp, guard)
	if err != nil {
		t.Fatalf("NewExecutionService: %v", err)
	}
	t.Cleanup(func() { _ = exec.Shutdown(2 * time.Second) })

	wf, err := comp.CreateWorkflow("completedAt test", "", []composition.Node{
		{ID: "t", NodeTypeID: "trigger-manual", Kind: composition.KindTrigger, Position: composition.Position{X: 0, Y: 0}},
		{ID: "p", NodeTypeID: "process-inject-text", Kind: composition.KindProcess,
			Config:   map[string]string{"text": "TAG", "placement": "append"},
			Position: composition.Position{X: 0, Y: 100}},
	}, []composition.Edge{
		{ID: "e1", Source: "t", Target: "p"},
	})
	if err != nil {
		t.Fatalf("CreateWorkflow: %v", err)
	}

	before := time.Now().Add(-time.Second)
	summary, err := exec.RunWorkflowWithPayload(wf.ID, RunKindTest, nil, "seed text")
	if err != nil {
		t.Fatalf("RunWorkflowWithPayload: %v", err)
	}
	final := waitFor(t, "run to succeed", 10*time.Second, func() (RunSummary, bool) {
		s, err := exec.summaryFor(summary.RunID)
		if err != nil || (s.Status != "SUCCESS" && s.Status != "ERROR") {
			return RunSummary{}, false
		}
		return s, true
	})
	if final.Status != "SUCCESS" {
		t.Fatalf("run status = %q, want SUCCESS (error: %s)", final.Status, final.Error)
	}

	detail, err := exec.GetRun(summary.RunID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}

	var injectStep *RunStep
	for i := range detail.Steps {
		if detail.Steps[i].NodeID == "p" {
			injectStep = &detail.Steps[i]
		}
	}
	if injectStep == nil {
		t.Fatalf("GetRun did not report the inject step: %+v", detail.Steps)
	}
	if injectStep.Status != "succeeded" {
		t.Fatalf("inject step status = %q, want succeeded", injectStep.Status)
	}
	if injectStep.CompletedAt.IsZero() {
		t.Error("CompletedAt is zero on an executed step, want the moment it finished")
	}
	if injectStep.CompletedAt.Before(before) {
		t.Errorf("CompletedAt = %v, want no earlier than this test's own start %v", injectStep.CompletedAt, before)
	}
}
