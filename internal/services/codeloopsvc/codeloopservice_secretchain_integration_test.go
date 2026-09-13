package codeloopsvc_test

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alicoding/mill/internal/adapters/credential"
	"github.com/alicoding/mill/internal/adapters/secretaudit"
	"github.com/alicoding/mill/internal/adapters/secretvault"
	"github.com/alicoding/mill/internal/domain/composition"
	"github.com/alicoding/mill/internal/domain/secret"
	"github.com/alicoding/mill/internal/services/codeloopsvc"
	"github.com/alicoding/mill/internal/services/compositionsvc"
	"github.com/alicoding/mill/internal/services/executionsvc"
	"github.com/alicoding/mill/internal/services/guardrailsvc"
	"github.com/alicoding/mill/internal/services/secretsvc"
	"github.com/alicoding/mill/internal/services/servicetest"
	"github.com/alicoding/mill/internal/services/wiring"
)

// The goal 0240 S2 end-to-end proof: the REAL chain (composition's own
// seams) wired the SAME way wiring.WireCodingLoopSecrets wires
// production, run against the REAL seeded "Run from clipboard"
// workflow through a REAL ExecutionService -- not a look-alike fixture,
// mirroring executionsvc's own codingloop_seed_test.go shape.

func waitForT[T any](t *testing.T, what string, timeout time.Duration, poll func() (T, bool)) T {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if v, ok := poll(); ok {
			return v
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// newWiredStack builds one real comp/guard/exec/secrets stack and wires
// it exactly like main.go does (wiring.WireCodingLoopSecrets), so this
// test proves the PRODUCTION wiring, not a reimplementation of it.
func newWiredStack(t *testing.T) (*executionsvc.ExecutionService, *codeloopsvc.CodeLoopService, *secretsvc.SecretService) {
	t.Helper()
	store := servicetest.NewFakeStore()
	comp := compositionsvc.NewCompositionService(store)
	guard := guardrailsvc.NewGuardrailService(store, comp)
	dbPath := filepath.Join(t.TempDir(), "exec.db")
	exec, err := executionsvc.NewExecutionService("sqlite:"+dbPath, comp, guard)
	if err != nil {
		t.Fatalf("NewExecutionService: %v", err)
	}
	t.Cleanup(func() { _ = exec.Shutdown(2 * time.Second) })

	// Same stub/restore shape codingloop_seed_test.go's own stubNotifier
	// establishes: the seed's final apply-notify step otherwise fails
	// with "no notifier registered (yet)" outside real app wiring.
	composition.SetNotifier(func(string, string, string, []string) error { return nil })
	t.Cleanup(func() {
		composition.SetNotifier(func(string, string, string, []string) error { return fmt.Errorf("no notifier registered (yet)") })
	})

	secretService := secretsvc.NewSecretService(secretvault.New(filepath.Join(t.TempDir(), "secrets.kdbx")), credential.NewInMemory(), servicetest.NewFakeStore())
	t.Cleanup(secretService.StopAutoLock)
	auditPath := filepath.Join(t.TempDir(), "audit.db")
	if err := secretService.OpenAudit(auditPath, nil); err != nil {
		t.Fatalf("OpenAudit: %v", err)
	}
	t.Cleanup(func() { _ = secretService.CloseAudit() })
	if err := secretService.SetupVault(); err != nil {
		t.Fatalf("SetupVault: %v", err)
	}

	codeLoop := codeloopsvc.NewCodeLoopService(guard)
	codeLoop.SetExecutionService(exec)
	wiring.WireCodingLoopSecrets(codeLoop, secretService)

	return exec, codeLoop, secretService
}

func runAndApprove(t *testing.T, exec *executionsvc.ExecutionService, summary executionsvc.RunSummary) executionsvc.RunDetail {
	t.Helper()
	pending := waitForT(t, "pending approval", 10*time.Second, func() (*executionsvc.PendingApproval, bool) {
		d, err := exec.GetRun(summary.RunID)
		if err != nil || d.Pending == nil {
			return nil, false
		}
		return d.Pending, true
	})
	if err := exec.ResolveApproval(summary.RunID, pending.NodeID, true, nil, false); err != nil {
		t.Fatalf("ResolveApproval: %v", err)
	}
	waitForT(t, "run to finish", 15*time.Second, func() (bool, bool) {
		d, err := exec.GetRun(summary.RunID)
		if err != nil {
			return false, false
		}
		done := d.Status == "SUCCESS" || d.Status == "ERROR" || d.Status == "MAX_RECOVERY_ATTEMPTS_EXCEEDED"
		return done, done
	})
	detail, err := exec.GetRun(summary.RunID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	return detail
}

func shellStepOf(t *testing.T, detail executionsvc.RunDetail) executionsvc.RunStep {
	t.Helper()
	for _, s := range detail.Steps {
		if s.NodeTypeID == "process-shell-command" {
			return s
		}
	}
	t.Fatalf("run %s has no process-shell-command step", detail.RunID)
	return executionsvc.RunStep{}
}

// TestRunCommandBlock_VaultSecret_ResolvesAndLeavesAuditLine proves the
// chain's FIRST source end to end: a vault entry whose title matches the
// referenced placeholder resolves for real (the shell sees it), and the
// resolution leaves the goal-0203 audit line via
// secretaudit.ContextCodingLoopShell, attributed to this exact run.
func TestRunCommandBlock_VaultSecret_ResolvesAndLeavesAuditLine(t *testing.T) {
	exec, codeLoop, secretService := newWiredStack(t)

	entry, err := secretService.CreateSecret("Coding Loop Vault Secret Fixture", "", "vault-fixture-secret-value", "", "", nil, "", "", nil)
	if err != nil {
		t.Fatalf("CreateSecret: %v", err)
	}

	summary, err := codeLoop.RunCommandBlock(composition.CodingLoopWorkflowID, `echo "check=$CODING_LOOP_VAULT_SECRET_FIXTURE"`, nil)
	if err != nil {
		t.Fatalf("RunCommandBlock: %v", err)
	}
	detail := runAndApprove(t, exec, summary)
	step := shellStepOf(t, detail)
	if step.Status != "succeeded" {
		t.Fatalf("step status = %q (error: %q), want succeeded", step.Status, step.Error)
	}
	if strings.Contains(step.Output, "vault-fixture-secret-value") {
		t.Errorf("step output = %q, contains the raw vault value -- must be redacted", step.Output)
	}
	if !strings.Contains(step.Output, secret.RedactedPlaceholder) {
		t.Errorf("step output = %q, want the resolved-and-echoed vault value scrubbed", step.Output)
	}

	access, err := secretService.ListSecretAccess(secretsvc.ListSecretAccessRequest{EntryID: entry.ID})
	if err != nil {
		t.Fatalf("ListSecretAccess: %v", err)
	}
	var found bool
	for _, rec := range access.Records {
		if rec.Context == string(secretaudit.ContextCodingLoopShell) && rec.RunID == summary.RunID {
			found = true
			if rec.Outcome != string(secretaudit.OutcomeRead) {
				t.Errorf("audit record Outcome = %q, want read", rec.Outcome)
			}
			if rec.WorkflowID != composition.CodingLoopWorkflowID {
				t.Errorf("audit record WorkflowID = %q, want %q", rec.WorkflowID, composition.CodingLoopWorkflowID)
			}
		}
	}
	if !found {
		t.Errorf("no coding-loop-shell audit record found for run %s among %d records", summary.RunID, len(access.Records))
	}
}

// TestRunCommandBlock_TypedSecret_NeverAppearsInSavedRunRecord is the
// goal's own explicitly-required test: a secret typed at Confirm (no
// vault entry, no shell env var for it) must never appear in the run
// record GetRun returns, however the command echoes it back.
func TestRunCommandBlock_TypedSecret_NeverAppearsInSavedRunRecord(t *testing.T) {
	exec, codeLoop, _ := newWiredStack(t)

	const fixtureValue = "typed-at-confirm-fixture-should-never-be-saved"
	summary, err := codeLoop.RunCommandBlock(
		composition.CodingLoopWorkflowID,
		`echo "typed=$CODING_LOOP_TYPED_SECRET_FIXTURE"`,
		map[string]string{"CODING_LOOP_TYPED_SECRET_FIXTURE": fixtureValue},
	)
	if err != nil {
		t.Fatalf("RunCommandBlock: %v", err)
	}
	detail := runAndApprove(t, exec, summary)
	step := shellStepOf(t, detail)
	if step.Status != "succeeded" {
		t.Fatalf("step status = %q (error: %q), want succeeded", step.Status, step.Error)
	}

	// The whole saved record -- every step's Input/Output, the run's own
	// top-level Output -- must be clean, not just the one step this test
	// happens to check by name.
	if strings.Contains(step.Output, fixtureValue) {
		t.Fatalf("step.Output = %q, contains the typed secret value", step.Output)
	}
	if strings.Contains(step.Input, fixtureValue) {
		t.Fatalf("step.Input = %q, contains the typed secret value", step.Input)
	}
	if strings.Contains(detail.Output, fixtureValue) {
		t.Fatalf("detail.Output = %q, contains the typed secret value", detail.Output)
	}
	if !strings.Contains(step.Output, secret.RedactedPlaceholder) {
		t.Errorf("step.Output = %q, want the echoed typed value scrubbed to the redaction placeholder", step.Output)
	}
}

func TestRunCommandBlock_FailedTypedSecretOutputStaysRedacted(t *testing.T) {
	exec, codeLoop, _ := newWiredStack(t)

	const fixtureValue = "failed-typed-secret-fixture-should-never-be-saved"
	summary, err := codeLoop.RunCommandBlock(
		composition.CodingLoopWorkflowID,
		`printf 'typed=%s\n' "$CODING_LOOP_FAILED_SECRET_FIXTURE"; exit 7`,
		map[string]string{"CODING_LOOP_FAILED_SECRET_FIXTURE": fixtureValue},
	)
	if err != nil {
		t.Fatalf("RunCommandBlock: %v", err)
	}
	detail := runAndApprove(t, exec, summary)
	if detail.Status != "ERROR" {
		t.Fatalf("detail status = %q, want ERROR (error: %q)", detail.Status, detail.Error)
	}
	step := shellStepOf(t, detail)
	if step.Status != "failed" {
		t.Fatalf("step status = %q, want failed (error: %q)", step.Status, step.Error)
	}
	if strings.Contains(step.Output, fixtureValue) || strings.Contains(detail.Output, fixtureValue) {
		t.Fatalf("failed output retained a raw typed secret: step=%q detail=%q", step.Output, detail.Output)
	}
	if !strings.Contains(step.Output, secret.RedactedPlaceholder) || !strings.Contains(detail.Output, secret.RedactedPlaceholder) {
		t.Errorf("failed output = step %q / detail %q, want the redaction placeholder in both", step.Output, detail.Output)
	}
}
