package execution

import (
	"context"
	"fmt"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"
)

// RetrieveWorkflow returns the SDK's persisted-result polling handle for an
// existing run. Call it only from an external service context; a workflow-body
// context would checkpoint the retrieval as another workflow step.
func RetrieveWorkflow[R any](ctx Client, workflowID string) (WorkflowHandle[R], error) {
	return dbos.RetrieveWorkflow[R](ctx, workflowID)
}

// WithCancel derives an independently cancellable DBOS context. Cancelling it
// stops an observer wait; it does not write CANCELLED to the durable workflow.
func WithCancel(ctx Context) (Context, context.CancelFunc) {
	return dbos.WithCancel(ctx)
}

// WithTimeout derives a bounded DBOS context for the final exact status read.
func WithTimeout(ctx Context, timeout time.Duration) (Context, context.CancelFunc) {
	return dbos.WithTimeout(ctx, timeout)
}

// WorkflowByID performs one exact-ID status read. loadOutput is explicit so a
// caller cannot accidentally treat an unloaded output as an empty result.
func WorkflowByID(ctx Client, workflowID string, loadOutput bool) (WorkflowStatus, error) {
	statuses, err := dbos.ListWorkflows(
		ctx,
		dbos.WithFilterWorkflowIDs(workflowID),
		dbos.WithFilterLoadInput(false),
		dbos.WithFilterLoadOutput(loadOutput),
		dbos.WithFilterLimit(1),
	)
	if err != nil {
		return WorkflowStatus{}, err
	}
	if len(statuses) != 1 || statuses[0].ID != workflowID {
		return WorkflowStatus{}, fmt.Errorf("workflow %q: expected one exact status row, got %d", workflowID, len(statuses))
	}
	return statuses[0], nil
}

const (
	WorkflowStatusSuccess                     = dbos.WorkflowStatusSuccess
	WorkflowStatusError                       = dbos.WorkflowStatusError
	WorkflowStatusMaxRecoveryAttemptsExceeded = dbos.WorkflowStatusMaxRecoveryAttemptsExceeded
)
