package configuresvc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/alicoding/mill/internal/adapters/aiclient"
	"github.com/alicoding/mill/internal/adapters/secretaudit"
	"github.com/alicoding/mill/internal/adapters/windowing"
	"github.com/alicoding/mill/internal/domain/aiprovider"
	"github.com/google/uuid"
)

const (
	availabilityMachineIDKey        = "aiprovider-availability-machine-id"
	AIProviderAvailabilityEventName = "aiprovider-availability-changed"
	providerInspectionTimeout       = 15 * time.Second
)

// AIProviderAvailabilityChanged is the typed event payload. Reports stay
// behind Get/List, so an event can never accidentally broadcast local evidence.
type AIProviderAvailabilityChanged struct {
	ProviderID string `json:"providerId"`
	CheckID    string `json:"checkId"`
}

// ProviderCheckPermissionRequest is the narrow seam main wires to the root
// GuardrailService. It deliberately carries no credential or response data.
type ProviderCheckPermissionRequest struct {
	ProviderID string
	CheckID    string
	Endpoint   string
	Actor      string
}

type providerCheckWorker struct {
	checkID  string
	revision string
	cancel   context.CancelFunc
}

func (c *ConfigureService) initAIProviderAvailability() {
	c.availabilityReports = make(map[string]aiprovider.Report)
	c.availabilityWorkers = make(map[string]providerCheckWorker)
	c.availabilityGenerations = make(map[string]uint64)
	c.availabilitySessionID = uuid.NewString()
	if id, ok := c.store.Get(availabilityMachineIDKey).(string); ok && id != "" {
		c.availabilityMachineID = id
		return
	}
	c.availabilityMachineID = uuid.NewString()
	_ = c.store.Set(availabilityMachineIDKey, c.availabilityMachineID)
}

// SetAIProviderCheckAuthorizer wires the one root guardrail door. It is a
// package function because exported ConfigureService methods are Wails RPCs.
func SetAIProviderCheckAuthorizer(c *ConfigureService, fn func(context.Context, ProviderCheckPermissionRequest) (aiprovider.PermissionResult, error)) {
	c.availabilityMu.Lock()
	c.providerCheckAuthorizer = fn
	c.availabilityMu.Unlock()
}

// StartAIProviderCheck starts one asynchronous metadata inspection.
func (c *ConfigureService) StartAIProviderCheck(id string) aiprovider.Report {
	return StartAIProviderCheckWithActor(c, id, "configure")
}

// StartAIProviderCheckWithActor preserves the caller identity for guardrail and
// secret audit evidence while delegating to the same coordinator as Wails. It
// is deliberately not a method, so a caller cannot select its actor over RPC.
func StartAIProviderCheckWithActor(c *ConfigureService, id, actor string) aiprovider.Report {
	c.availabilityMu.Lock()
	c.mu.Lock()
	p, ok := c.aiProviderSnapshotLocked(id)
	c.mu.Unlock()
	if !ok {
		c.availabilityMu.Unlock()
		return missingProviderReport(id, c.availabilityMachineID, c.availabilitySessionID)
	}
	if c.availabilityClosed {
		report := c.initialAvailabilityReport(p, "", c.aiProviderConfigRevisionLocked(p), "")
		report.Lifecycle = aiprovider.CheckCancelled
		report.Freshness = aiprovider.FreshnessStale
		report.ReasonCodes = append(report.ReasonCodes, "service-stopped")
		c.availabilityMu.Unlock()
		return report
	}
	revision := c.aiProviderConfigRevisionLocked(p)
	checkID := uuid.NewString()
	endpoint, _ := aiclient.InspectionEndpoint(aiclient.Kind(p.Kind), p.BaseURL)
	report := c.initialAvailabilityReport(p, checkID, revision, endpoint)
	report.Lifecycle = aiprovider.CheckAwaitingApproval

	ctx, cancel := context.WithCancel(context.Background())
	if old, exists := c.availabilityWorkers[id]; exists {
		old.cancel()
	}
	c.availabilityReports[id] = report
	c.availabilityWorkers[id] = providerCheckWorker{checkID: checkID, revision: revision, cancel: cancel}
	c.availabilityMu.Unlock()
	c.emitAIProviderAvailabilityChanged(id, checkID)
	go c.runAIProviderCheck(ctx, p, report, actor)
	return c.projectAIProviderSampleEvidence(report)
}

// GetAIProviderAvailability is a cache-only read.
func (c *ConfigureService) GetAIProviderAvailability(id string) aiprovider.Report {
	c.availabilityMu.Lock()
	report, ok := c.availabilityReports[id]
	if ok {
		if report.ConfigRevision != "" && !c.aiProviderRevisionCurrentLocked(id, report.ConfigRevision) {
			if worker, active := c.availabilityWorkers[id]; active {
				worker.cancel()
				delete(c.availabilityWorkers, id)
				report.Lifecycle = aiprovider.CheckCancelled
			}
			report.Freshness = aiprovider.FreshnessStale
			report.ReasonCodes = appendUnique(report.ReasonCodes, "configuration-changed")
			c.availabilityReports[id] = report
		}
		c.availabilityMu.Unlock()
		return c.projectAIProviderSampleEvidence(report)
	}
	c.availabilityMu.Unlock()
	p, exists := c.aiProviderSnapshot(id)
	if !exists {
		return missingProviderReport(id, c.availabilityMachineID, c.availabilitySessionID)
	}
	endpoint, _ := aiclient.InspectionEndpoint(aiclient.Kind(p.Kind), p.BaseURL)
	return c.projectAIProviderSampleEvidence(c.initialAvailabilityReport(p, "", c.aiProviderConfigRevision(p), endpoint))
}

// ListAIProviderAvailability is a cache-only read with one row per provider.
func (c *ConfigureService) ListAIProviderAvailability() []aiprovider.Report {
	providers := c.AIProviders()
	out := make([]aiprovider.Report, 0, len(providers))
	for _, p := range providers {
		out = append(out, c.GetAIProviderAvailability(p.ID))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ProviderID < out[j].ProviderID })
	return out
}

// CancelAIProviderCheck cancels approval, retry waits, or the active request.
func (c *ConfigureService) CancelAIProviderCheck(id, checkID string) aiprovider.Report {
	c.availabilityMu.Lock()
	report, ok := c.availabilityReports[id]
	worker, active := c.availabilityWorkers[id]
	if !ok || report.CheckID != checkID || !active || worker.checkID != checkID {
		c.availabilityMu.Unlock()
		if ok {
			return c.projectAIProviderSampleEvidence(report)
		}
		return c.GetAIProviderAvailability(id)
	}
	worker.cancel()
	report.Lifecycle = aiprovider.CheckCancelled
	report.Freshness = aiprovider.FreshnessStale
	report.ReasonCodes = appendUnique(report.ReasonCodes, "check-cancelled")
	c.availabilityReports[id] = report
	delete(c.availabilityWorkers, id)
	c.availabilityMu.Unlock()
	c.emitAIProviderAvailabilityChanged(id, checkID)
	return c.projectAIProviderSampleEvidence(report)
}

// InvalidateAIProviderAvailability cancels and marks local evidence stale. It
// is an internal package function rather than a bound service method.
func InvalidateAIProviderAvailability(c *ConfigureService, id string) {
	c.availabilityMu.Lock()
	c.availabilityGenerations[id]++
	worker, active := c.availabilityWorkers[id]
	if active {
		worker.cancel()
		delete(c.availabilityWorkers, id)
	}
	report, ok := c.availabilityReports[id]
	if ok {
		report.Freshness = aiprovider.FreshnessStale
		report.ReasonCodes = appendUnique(report.ReasonCodes, "configuration-changed")
		if active {
			report.Lifecycle = aiprovider.CheckCancelled
		}
		c.availabilityReports[id] = report
	}
	c.availabilityMu.Unlock()
	if ok {
		c.emitAIProviderAvailabilityChanged(id, report.CheckID)
	}
}

// InvalidateAIProviderAvailabilityForSecrets conservatively invalidates every
// report because source-backed references cannot be mapped without a secret read.
func InvalidateAIProviderAvailabilityForSecrets(c *ConfigureService) {
	c.availabilityMu.Lock()
	c.availabilitySecretEpoch++
	events := make([]AIProviderAvailabilityChanged, 0, len(c.availabilityReports))
	for id, report := range c.availabilityReports {
		if worker, ok := c.availabilityWorkers[id]; ok {
			worker.cancel()
			delete(c.availabilityWorkers, id)
			report.Lifecycle = aiprovider.CheckCancelled
		}
		report.Freshness = aiprovider.FreshnessStale
		report.ReasonCodes = appendUnique(report.ReasonCodes, "secret-source-changed")
		c.availabilityReports[id] = report
		events = append(events, AIProviderAvailabilityChanged{ProviderID: id, CheckID: report.CheckID})
	}
	c.availabilityMu.Unlock()
	for _, event := range events {
		c.emitAIProviderAvailabilityChanged(event.ProviderID, event.CheckID)
	}
}

// StopAIProviderChecks cancels every process-local worker during shutdown.
func StopAIProviderChecks(c *ConfigureService) {
	c.availabilityMu.Lock()
	c.availabilityClosed = true
	c.availabilitySecretEpoch++
	ids := make([]string, 0, len(c.availabilityReports))
	for id, report := range c.availabilityReports {
		if worker, ok := c.availabilityWorkers[id]; ok {
			worker.cancel()
			delete(c.availabilityWorkers, id)
			report.Lifecycle = aiprovider.CheckCancelled
		}
		report.Freshness = aiprovider.FreshnessStale
		report.ReasonCodes = appendUnique(report.ReasonCodes, "service-stopped")
		c.availabilityReports[id] = report
		ids = append(ids, id)
	}
	c.availabilityMu.Unlock()
	for _, id := range ids {
		c.emitAIProviderAvailabilityChanged(id, c.GetAIProviderAvailability(id).CheckID)
	}
}

func (c *ConfigureService) runAIProviderCheck(ctx context.Context, p aiprovider.AIProvider, report aiprovider.Report, actor string) {
	c.availabilityMu.Lock()
	authorize := c.providerCheckAuthorizer
	c.availabilityMu.Unlock()
	if authorize == nil {
		report.Permission.Status = aiprovider.PermissionDenied
		report.Lifecycle = aiprovider.CheckCompleted
		report.ReasonCodes = append(report.ReasonCodes, "policy-service-unwired")
		c.finishAIProviderCheck(report)
		return
	}
	permission, err := authorize(ctx, ProviderCheckPermissionRequest{ProviderID: p.ID, CheckID: report.CheckID, Endpoint: report.CheckedEndpoint, Actor: actor})
	if err != nil {
		c.finishProviderCheckAuthorizationError(report, err)
		return
	}
	report.Permission = permission
	if permission.Status != aiprovider.PermissionAllowed {
		report.Lifecycle = aiprovider.CheckCompleted
		report.ReasonCodes = append(report.ReasonCodes, "permission-denied")
		c.finishAIProviderCheck(report)
		return
	}
	report.Lifecycle = aiprovider.CheckChecking
	report.Transport = aiprovider.TransportChecking
	if !c.updateAIProviderCheck(report) || ctx.Err() != nil {
		return
	}

	apiKey, err := c.resolveOptionalSecretRef(p.Label, fieldAIProviderKey, p.KeyRef, secretaudit.AccessContext{Context: secretaudit.ContextAIProvider, Actor: "provider-check:" + report.CheckID + ":" + actor})
	if err != nil {
		report.Lifecycle = aiprovider.CheckCompleted
		report.ReasonCodes = append(report.ReasonCodes, "secret-resolution-failed")
		c.finishAIProviderCheck(report)
		return
	}
	networkCtx, cancel := context.WithTimeout(ctx, providerInspectionTimeout)
	defer cancel()
	result, err := aiclient.Inspect(aiclient.InspectionRequest{Kind: aiclient.Kind(p.Kind), BaseURL: p.BaseURL, Model: p.Model, APIKey: apiKey, Context: networkCtx}) //nolint:contextcheck // InspectionRequest.Context is forwarded by the adapter.
	if err != nil {
		c.finishCancelledOrTimedOut(report, err)
		return
	}
	report.CheckedAt = time.Now().UTC()
	report.CheckedEndpoint = result.Endpoint
	report.Transport = aiprovider.TransportStatus(result.Transport)
	report.Inspection = aiprovider.InspectionStatus(result.Inspection)
	report.Authentication = aiprovider.AuthenticationStatus(result.Authentication)
	report.ModelChoices = make([]aiprovider.ModelChoice, 0, len(result.Models))
	for _, id := range result.Models {
		report.ModelChoices = append(report.ModelChoices, aiprovider.ModelChoice{ID: id})
	}
	report.ModelInventoryComplete = result.InventoryComplete
	report.SelectedModelFound = result.SelectedFound
	report.ReasonCodes = append(report.ReasonCodes, result.ReasonCodes...)
	report.Freshness = aiprovider.FreshnessFresh
	report.Lifecycle = aiprovider.CheckCompleted
	c.finishAIProviderCheck(report)
}

func (c *ConfigureService) finishProviderCheckAuthorizationError(report aiprovider.Report, err error) {
	if errors.Is(err, context.Canceled) {
		c.finishCancelledOrTimedOut(report, err)
		return
	}
	if errors.Is(err, context.DeadlineExceeded) || strings.Contains(strings.ToLower(err.Error()), "timed out") {
		report.Lifecycle = aiprovider.CheckTimedOut
		report.Freshness = aiprovider.FreshnessStale
		report.ReasonCodes = append(report.ReasonCodes, "check-timed-out")
		c.finishAIProviderCheck(report)
		return
	}
	report.Permission.Status = aiprovider.PermissionDenied
	report.Lifecycle = aiprovider.CheckCompleted
	report.ReasonCodes = append(report.ReasonCodes, "policy-check-failed")
	c.finishAIProviderCheck(report)
}

func (c *ConfigureService) finishCancelledOrTimedOut(report aiprovider.Report, err error) {
	if errors.Is(err, context.DeadlineExceeded) {
		report.Lifecycle = aiprovider.CheckTimedOut
		report.ReasonCodes = append(report.ReasonCodes, "check-timed-out")
	} else {
		report.Lifecycle = aiprovider.CheckCancelled
		report.ReasonCodes = append(report.ReasonCodes, "check-cancelled")
	}
	report.Freshness = aiprovider.FreshnessStale
	c.finishAIProviderCheck(report)
}

func (c *ConfigureService) updateAIProviderCheck(report aiprovider.Report) bool {
	c.availabilityMu.Lock()
	worker, ok := c.availabilityWorkers[report.ProviderID]
	if !ok || worker.checkID != report.CheckID || worker.revision != report.ConfigRevision || !c.aiProviderRevisionCurrentLocked(report.ProviderID, report.ConfigRevision) {
		c.availabilityMu.Unlock()
		return false
	}
	c.availabilityReports[report.ProviderID] = report
	c.availabilityMu.Unlock()
	c.emitAIProviderAvailabilityChanged(report.ProviderID, report.CheckID)
	return true
}

func (c *ConfigureService) finishAIProviderCheck(report aiprovider.Report) {
	c.availabilityMu.Lock()
	worker, ok := c.availabilityWorkers[report.ProviderID]
	if !ok || worker.checkID != report.CheckID || worker.revision != report.ConfigRevision || !c.aiProviderRevisionCurrentLocked(report.ProviderID, report.ConfigRevision) {
		c.availabilityMu.Unlock()
		return
	}
	c.availabilityReports[report.ProviderID] = report
	delete(c.availabilityWorkers, report.ProviderID)
	c.availabilityMu.Unlock()
	c.emitAIProviderAvailabilityChanged(report.ProviderID, report.CheckID)
}

func (c *ConfigureService) aiProviderSnapshot(id string) (aiprovider.AIProvider, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.aiProviderSnapshotLocked(id)
}

func (c *ConfigureService) aiProviderSnapshotLocked(id string) (aiprovider.AIProvider, bool) {
	for _, p := range c.aiProviders {
		if p.ID == id {
			return p, true
		}
	}
	return aiprovider.AIProvider{}, false
}

func (c *ConfigureService) aiProviderConfigRevision(p aiprovider.AIProvider) string {
	c.availabilityMu.Lock()
	defer c.availabilityMu.Unlock()
	return c.aiProviderConfigRevisionLocked(p)
}

func (c *ConfigureService) aiProviderConfigRevisionLocked(p aiprovider.AIProvider) string {
	epoch := c.availabilitySecretEpoch
	generation := c.availabilityGenerations[p.ID]
	payload, _ := json.Marshal(struct {
		ID, Label              string
		Kind                   aiprovider.Kind
		BaseURL, Model, KeyRef string
		UpdatedAt              time.Time
		AdapterVersion         int
		SecretEpoch            uint64
		Generation             uint64
	}{p.ID, p.Label, p.Kind, p.BaseURL, p.Model, p.KeyRef, p.UpdatedAt.UTC(), aiprovider.AvailabilityAdapterVersion, epoch, generation})
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func (c *ConfigureService) aiProviderRevisionCurrentLocked(id, revision string) bool {
	c.mu.Lock()
	p, ok := c.aiProviderSnapshotLocked(id)
	c.mu.Unlock()
	return ok && c.aiProviderConfigRevisionLocked(p) == revision
}

func (c *ConfigureService) initialAvailabilityReport(p aiprovider.AIProvider, checkID, revision, endpoint string) aiprovider.Report {
	return aiprovider.Report{ProviderID: p.ID, CheckID: checkID, ConfigRevision: revision, Model: p.Model, AdapterVersion: aiprovider.AvailabilityAdapterVersion, MachineID: c.availabilityMachineID, SessionID: c.availabilitySessionID, CheckedEndpoint: endpoint, Transport: aiprovider.TransportUnchecked, Inspection: aiprovider.InspectionNotChecked, Authentication: aiprovider.AuthenticationUnknown, Permission: aiprovider.PermissionResult{Status: aiprovider.PermissionUnchecked}, Operations: unknownOperations(p.Kind), ReasonCodes: []string{}, Freshness: aiprovider.FreshnessNotChecked, Lifecycle: aiprovider.CheckNotStarted, ModelChoices: []aiprovider.ModelChoice{}}
}

func unknownOperations(kind aiprovider.Kind) []aiprovider.OperationFeature {
	wires := map[aiprovider.Operation]string{}
	if kind == aiprovider.KindAnthropic {
		wires[aiprovider.OperationText] = "POST /v1/messages text"
		wires[aiprovider.OperationStructured] = "POST /v1/messages forced tool input"
		wires[aiprovider.OperationClassification] = "POST /v1/messages forced tool input"
	} else {
		wires[aiprovider.OperationText] = "POST /v1/chat/completions text"
		wires[aiprovider.OperationStructured] = "POST /v1/chat/completions strict JSON schema"
		wires[aiprovider.OperationClassification] = "POST /v1/chat/completions strict JSON schema"
	}
	out := make([]aiprovider.OperationFeature, 0, 3)
	for _, op := range []aiprovider.Operation{aiprovider.OperationText, aiprovider.OperationStructured, aiprovider.OperationClassification} {
		out = append(out, aiprovider.OperationFeature{Operation: op, Support: aiprovider.SupportUnknown, Evidence: aiprovider.EvidenceProviderMetadata, WireOperation: wires[op], ReasonCodes: []string{"operation-not-proven-by-metadata"}})
	}
	return out
}

func missingProviderReport(id, machineID, sessionID string) aiprovider.Report {
	return aiprovider.Report{ProviderID: id, AdapterVersion: aiprovider.AvailabilityAdapterVersion, MachineID: machineID, SessionID: sessionID, Transport: aiprovider.TransportInvalidConfiguration, Inspection: aiprovider.InspectionFailed, Authentication: aiprovider.AuthenticationUnknown, Permission: aiprovider.PermissionResult{Status: aiprovider.PermissionUnchecked}, ReasonCodes: []string{"provider-not-found"}, Freshness: aiprovider.FreshnessNotChecked, Lifecycle: aiprovider.CheckCompleted, Operations: []aiprovider.OperationFeature{}, ModelChoices: []aiprovider.ModelChoice{}}
}

func cloneAvailabilityReport(report aiprovider.Report) aiprovider.Report {
	report.ReasonCodes = append([]string(nil), report.ReasonCodes...)
	report.Operations = append([]aiprovider.OperationFeature(nil), report.Operations...)
	for i := range report.Operations {
		report.Operations[i].ReasonCodes = append([]string(nil), report.Operations[i].ReasonCodes...)
		report.Operations[i].LastSampleAttempt = cloneSampleEvidence(report.Operations[i].LastSampleAttempt)
		report.Operations[i].LastSampleSuccess = cloneSampleEvidence(report.Operations[i].LastSampleSuccess)
	}
	report.ModelChoices = append([]aiprovider.ModelChoice(nil), report.ModelChoices...)
	if report.SelectedModelFound != nil {
		v := *report.SelectedModelFound
		report.SelectedModelFound = &v
	}
	return report
}

func cloneSampleEvidence(evidence *aiprovider.SampleEvidence) *aiprovider.SampleEvidence {
	if evidence == nil {
		return nil
	}
	copy := *evidence
	return &copy
}
func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

var AIProviderAvailabilityTestHook func(AIProviderAvailabilityChanged)

func (c *ConfigureService) emitAIProviderAvailabilityChanged(providerID, checkID string) {
	evt := AIProviderAvailabilityChanged{ProviderID: providerID, CheckID: checkID}
	windowing.Emit(AIProviderAvailabilityEventName, evt)
	if AIProviderAvailabilityTestHook != nil {
		AIProviderAvailabilityTestHook(evt)
	}
}
