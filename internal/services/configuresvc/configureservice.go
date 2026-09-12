// Package configuresvc is the Wails-facing layer over Configure-authored
// data: HTTPRequests, Lists, MCP servers, execution environments, AI
// providers, and Decision/DeclaredStepType definitions -- the
// integration-rules half of "business rules on canvas, integration
// rules in Configure" (.claude/rules/architecture.md). Each entity type
// owns its own persistence under one settings-store JSON blob per kind,
// plus the seed/reconcile lifecycle shared with compositionsvc.
package configuresvc

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/alicoding/mill/internal/adapters/credential"
	"github.com/alicoding/mill/internal/adapters/httpconnector"
	"github.com/alicoding/mill/internal/adapters/openapispec"
	"github.com/alicoding/mill/internal/adapters/secretaudit"
	"github.com/alicoding/mill/internal/adapters/settings"
	"github.com/alicoding/mill/internal/domain/aiprovider"
	"github.com/alicoding/mill/internal/domain/clientcert"
	"github.com/alicoding/mill/internal/domain/composition"
	"github.com/alicoding/mill/internal/domain/conversionprofile"
	"github.com/alicoding/mill/internal/domain/decision"
	"github.com/alicoding/mill/internal/domain/declaredsteptype"
	"github.com/alicoding/mill/internal/domain/environment"
	"github.com/alicoding/mill/internal/domain/execenv"
	"github.com/alicoding/mill/internal/domain/httprequest"
	"github.com/alicoding/mill/internal/domain/list"
	"github.com/alicoding/mill/internal/domain/mcpserver"
	"github.com/alicoding/mill/internal/domain/reference"
	"github.com/alicoding/mill/internal/domain/secret"
	"github.com/alicoding/mill/internal/domain/secretsource"
	"github.com/alicoding/mill/internal/services/compositionsvc"
)

// validateOpenAPISpec rejects an HTTPRequest save whose OpenAPISpec
// field doesn't parse -- an empty spec is valid (ADR-0007: OpenAPISpec
// is optional, a request with none behaves exactly as before this
// field existed). Parsing/validating a request's raw spec text is a
// commodity-adapter concern (internal/adapters/openapispec), not core
// domain, so it lives here at the service layer rather than inside
// httprequest.Validate -- internal/domain/httprequest stays pure per
// CLAUDE.md's domain-purity rule, same reasoning ConfigureService
// already applies to c.credentials.Delete/Set below.
func validateOpenAPISpec(spec string) error {
	if spec == "" {
		return nil
	}
	if _, err := openapispec.Parse([]byte(spec)); err != nil {
		return fmt.Errorf("OpenAPI spec: %w", err)
	}
	return nil
}

// requestsKey/listsKey mirror workflowsKey's shape (compositionservice.go):
// one atomic JSON blob per entity kind, sharing the same settings.json
// file rather than a new store/file per entity. requestsKey renamed
// from connectorsKey by ADR-0016 -- restore() below migrates
// already-persisted data forward from the old key, since (unlike
// composition-workflows -> -v2's own prototype-data precedent) this
// key holds real current data on a real machine, not throwaway data
// safe to silently drop.
const (
	requestsKey         = "configure-requests"
	legacyConnectorsKey = "configure-connectors"
	listsKey            = "configure-lists"
)

// ConfigureService is the Wails-facing layer over Configure-authored data
// (docs/SPEC.md §3.5): HTTPRequests, Lists, and (delegated to
// CompositionService) a workflow's Attributes schema. Mirrors
// CompositionService's own shape -- state + persistence a stateless
// domain package can't own, no domain logic of its own.
//
// It also owns wiring composition.go's request-lookup and list-lookup
// seams (SetHTTPRequestLookup/SetListLookup) to its own resolve*
// methods -- composition.go doesn't (and shouldn't) import this
// package directly, same reasoning as CompositionService's Syncer
// interface for TriggerService.
type ConfigureService struct {
	mu            sync.Mutex
	undo          deleteUndo
	store         settings.Store
	credentials   credential.Store
	requests      []httprequest.HTTPRequest
	lists         []list.List
	mcpServers    []mcpserver.MCPServer
	decisions     []decision.Decision
	execEnvs      []execenv.ExecEnv
	environments  []environment.Environment
	secretSources []secretsource.Source
	// seedAssetsDir is where a file-backed seed asset (goal 0367's
	// example dotenv file) is written; empty until wiring provides it,
	// and the asset-backed golden stays unseeded until then (atlas's
	// SetCapturesDir deferral is the template).
	seedAssetsDir      string
	conversionProfiles []conversionprofile.Profile
	clientCerts        []clientcert.ClientCertificate
	// clientCertStatuses caches one decoded status per entity revision
	// (clientcert_resolve.go): the list reads a status per row, and a
	// decode per render would re-read the vault every time.
	clientCertStatuses clientCertStatusCache
	aiProviders        []aiprovider.AIProvider
	declaredStepTypes  []declaredsteptype.DeclaredStepType
	composition        *compositionsvc.CompositionService
	// secretResolver resolves a vault entry id to its password (goal
	// 0185 S3) -- wired late via SetSecretResolver once secretsvc's
	// SecretService exists (main.go/wiring.go construct it after this
	// service). Defaults to an error so an Env "vault:" reference
	// resolved before wiring fails loudly rather than silently
	// returning an empty secret. Takes a secretaudit.AccessContext (goal
	// 0203 S3) -- ResolveSecretValue on the other side of this seam is
	// where the actual audit line gets written, so every call site here
	// carries who's asking.
	secretResolver func(id string, actx secretaudit.AccessContext) (string, error)
	// secretUnresolvedLookup answers whether a source-backed reference
	// still resolves (goal 0408 S1) -- RequestSecretUnresolved's own
	// seam, wired late via SetSecretUnresolvedLookup once secretsvc
	// exists. Defaults to reporting nothing unresolved, so a check run
	// before wiring completes never blocks a run on a false positive.
	secretUnresolvedLookup func(ref string) (unresolved bool, key, sourceLabel string)
	// secretTrashedLookup answers whether a vault-backed reference
	// currently names an entry in Trash (goal 0406) -- RequestSecretTrashed's
	// own seam, wired late via SetSecretTrashedLookup once secretsvc
	// exists. Defaults to reporting nothing trashed, the same
	// never-block-on-a-false-positive-before-wiring posture
	// secretUnresolvedLookup gives.
	secretTrashedLookup func(ref string) (trashed bool, label string)
	// secretSourcesChanged tells secretsvc's watch set to recompute
	// itself (goal 0408 S1) after a secret source is created, edited or
	// deleted -- wired late via SetSecretSourcesChanged, nil-safe no-op
	// until then.
	secretSourcesChanged func()
	// secretCreator is the store's own create door, wired late the same
	// way secretResolver is -- used only by the adoption pass
	// (configureservice_secretadoption.go), nil until it is wired.
	secretCreator SecretCreator
	// secretLabelsLister lists every vault entry's Summary (Title, no
	// Password) -- DeriveSecretLabels' own read (goal 0203 S2), wired
	// late via SetSecretLabelsLister the same way secretResolver is.
	// Defaults to reporting no titles known at all, the same treatment
	// a currently-locked vault gets: DeriveSecretLabels' own
	// unknownVaultLabel placeholder covers both, since a derivation
	// that can't currently see a title must still answer SOMETHING,
	// never error, for a workflow that isn't even running yet.
	secretLabelsLister func() ([]secret.Summary, error)
	// recordUndo journals a List edit or an entity delete into the app's
	// one actor-scoped undo journal (ADR-0044 -- configurelistundo.go and
	// configureentityundo.go hold the entry families), wired late via
	// WireUndoJournal. Nil until wired, which leaves every door below
	// recording nothing.
	recordUndo undoRecorder
	// boardRefs is atlassvc's own board-object reference index
	// (goal 0392 S1) -- wired late via WireBoardReferenceLookup, the
	// same nil-means-off discipline every cross-service seam here
	// follows. References() (configureservice_refintegrity.go) treats a
	// nil boardRefs as "no board ever references anything" rather than
	// panicking, so a test that never wires it still exercises the
	// workflow half.
	boardRefs func(entityKind, id string) []reference.ObjectRef
	// pluginRefs is pluginsvc's own entityRef-setting reference index
	// (docs/goals/0400) -- wired late via WirePluginReferenceLookup, the
	// same nil-means-off discipline boardRefs above follows.
	pluginRefs               func(entityKind, id string) []reference.PluginRef
	availabilityMu           sync.Mutex
	availabilityReports      map[string]aiprovider.Report
	availabilityWorkers      map[string]providerCheckWorker
	availabilityGenerations  map[string]uint64
	availabilitySecretEpoch  uint64
	availabilityMachineID    string
	availabilitySessionID    string
	aiProviderMutation       aiProviderMutationCoordinator
	aiProviderImpact         aiProviderImpactLookup
	availabilityClosed       bool
	providerCheckAuthorizer  func(context.Context, ProviderCheckPermissionRequest) (aiprovider.PermissionResult, error)
	aiProviderSampleEvidence *aiProviderSampleEvidenceState
}

// WireBoardReferenceLookup injects atlassvc's ObjectsReferencing.
// Called once from wiring.go, after both services exist.
//
//wails:ignore
func (c *ConfigureService) WireBoardReferenceLookup(fn func(entityKind, id string) []reference.ObjectRef) {
	c.boardRefs = fn
}

// WirePluginReferenceLookup injects pluginsvc's PluginsReferencing.
// Called once from wiring.go, after both services exist.
//
//wails:ignore
func (c *ConfigureService) WirePluginReferenceLookup(fn func(entityKind, id string) []reference.PluginRef) {
	c.pluginRefs = fn
}

// SetSecretResolver wires ConfigureService's own vault-reference
// resolution (mcpserver.EnvVaultRef) to secretsvc.SecretService's
// ResolveSecretValue -- called once from main.go after that service
// exists. Exported for wiring only, never a frontend RPC.
//
//wails:ignore
func (c *ConfigureService) SetSecretResolver(fn func(id string, actx secretaudit.AccessContext) (string, error)) {
	c.secretResolver = fn
}

// SetSecretUnresolvedLookup wires RequestSecretUnresolved's own check
// to secretsvc.SecretService's SecretRefUnresolved (goal 0408 S1) --
// called once from main.go after that service exists, same pattern as
// SetSecretResolver.
//
//wails:ignore
func (c *ConfigureService) SetSecretUnresolvedLookup(fn func(ref string) (unresolved bool, key, sourceLabel string)) {
	c.secretUnresolvedLookup = fn
}

// SetSecretTrashedLookup wires RequestSecretTrashed's own check to
// secretsvc.SecretService's SecretRefTrashed (goal 0406) -- called once
// from main.go after that service exists, same pattern as
// SetSecretUnresolvedLookup.
//
//wails:ignore
func (c *ConfigureService) SetSecretTrashedLookup(fn func(ref string) (trashed bool, label string)) {
	c.secretTrashedLookup = fn
}

// SetSecretSourcesChanged wires secret-source create/update/delete to
// secretsvc.SecretService's RearmSourceWatches (goal 0408 S1) -- called
// once from main.go after that service exists.
//
//wails:ignore
func (c *ConfigureService) SetSecretSourcesChanged(fn func()) {
	c.secretSourcesChanged = fn
}

// SetSecretLabelsLister wires DeriveSecretLabels' title lookup to
// secretsvc.SecretService's ListSecrets -- called once from main.go
// after that service exists, same pattern as SetSecretResolver.
// Exported for wiring only, never a frontend RPC.
//
//wails:ignore
func (c *ConfigureService) SetSecretLabelsLister(fn func() ([]secret.Summary, error)) {
	c.secretLabelsLister = fn
}

func NewConfigureService(store settings.Store, comp *compositionsvc.CompositionService, credentials credential.Store) *ConfigureService {
	// Wrapped once here so every credential read/write in this package
	// flows through the presence cache (credpresence.go) -- validation
	// asks about presence often, and only this choke point keeps the
	// cache truthful.
	c := &ConfigureService{store: store, composition: comp, credentials: credentials}
	c.initAIProviderAvailability()
	c.aiProviderSampleEvidence = newAIProviderSampleEvidenceState()
	c.secretResolver = func(id string, _ secretaudit.AccessContext) (string, error) {
		return "", fmt.Errorf("no vault secret resolver registered (yet) for id %q", id)
	}
	c.secretLabelsLister = func() ([]secret.Summary, error) { return nil, nil }
	c.restore()
	c.restoreMCPServers()
	c.restoreDecisions()
	c.restoreExecEnvs()
	c.restoreEnvironments()
	c.restoreSecretSources()
	c.restoreConversionProfiles()
	c.restoreClientCertificates()
	c.restoreAIProviders()
	c.restoreDeclaredStepTypes()
	// reconcileBuiltIn* (configureservice_builtin.go, docs/goals/0037)
	// supersede the old insert-only topUpBuiltIn*: insert/upgrade/
	// leave-alone/skip per golden, not just insert.
	c.reconcileBuiltInDecisions()
	c.reconcileBuiltInLists()
	c.reconcileBuiltInMCPServers()
	c.reconcileBuiltInExecEnvs()
	c.reconcileBuiltInEnvironments()
	c.reconcileBuiltInConversionProfiles()
	c.reconcileBuiltInClientCertificates()
	c.reconcileBuiltInDeclaredStepTypes()
	c.reconcileBuiltInSecretSources()
	// Client certificates reach the transport through this one seam
	// (goal 0306 S1): httpconnector asks per request, this service
	// answers from the vault. Wired here rather than in main.go for the
	// same reason every lookup above is -- httpconnector must not know
	// this package exists.
	httpconnector.SetClientTLS(c)
	composition.SetConversionProfileLookup(c.resolveConversionProfile)
	composition.SetHTTPRequestLookup(c.resolveHTTPRequest)
	composition.SetListLookup(c.resolveList)
	composition.SetApplyListRow(c.ApplyListRow)
	composition.SetApplyListSync(c.SyncListRows)
	composition.SetMCPServerLookup(c.resolveMCPServer)
	composition.SetDecisionLookup(c.resolveDecision)
	composition.SetExecEnvLookup(c.resolveExecEnv)
	composition.SetEnvironmentLookup(c.resolveEnvironment)
	composition.SetEnvironmentVarGapCheck(c.environmentVarGap)
	composition.SetAIProviderLookup(c.resolveAIProvider)
	composition.SetDeclaredNodeTypeLookup(c.declaredStepBindings)
	// Must run AFTER the provider above is wired -- goal 0054 slice A's
	// seeded workflow references a declared step type, only resolvable
	// once SetDeclaredNodeTypeLookup is live (see
	// reconcileDeclaredStepTypeSeedWorkflow's own doc comment for the
	// full construction-order reasoning).
	c.reconcileDeclaredStepTypeSeedWorkflow()
	return c
}

// resolveList/Lists/CreateList/UpdateList/DeleteList/persistLists/
// migrateLegacyLists live in configurelist.go (goal 0017 split, see
// that file's header comment); the row doors (AddListRow/AddListRowAt/
// UpdateListRow/DeleteListRow) live in configurelistrow.go beside
// their undo recording.

// --- Attributes (delegates to CompositionService -- see SPEC.md §3.5's
// "Configure-authored but workflow-scoped" cardinality note) ---

func (c *ConfigureService) UpdateWorkflowAttributes(workflowID string, attrs []composition.AttributeDef) (composition.Workflow, error) {
	return c.composition.UpdateAttributes(workflowID, attrs)
}

// restore loads persisted HTTPRequests/Lists. HTTPRequests has three
// cases, checked in order (ADR-0016's migration plan): (1) requestsKey
// already has data -- the common case after this migration has run
// once; (2) requestsKey is empty but the pre-rename legacyConnectorsKey
// has data -- a real machine's existing Connectors, migrated forward
// and persisted under the new key so this branch never fires again;
// (3) neither key has anything -- a genuinely fresh install, seeded
// with httprequest.BuiltIn()'s seven examples (docs/SPEC.md §4's
// Update) plus their demo secrets (seedBuiltInSecrets,
// configureservice_builtin.go), same lazy-seed-until-first-real-
// mutation shape CompositionService.restore() already established for
// Workflows.
func (c *ConfigureService) restore() {
	if raw, ok := c.store.Get(requestsKey).(string); ok && raw != "" {
		var requests []httprequest.HTTPRequest
		if err := json.Unmarshal([]byte(raw), &requests); err == nil {
			c.requests = requests
		}
	} else if raw, ok := c.store.Get(legacyConnectorsKey).(string); ok && raw != "" {
		var requests []httprequest.HTTPRequest
		if err := json.Unmarshal([]byte(raw), &requests); err == nil {
			c.requests = requests
			// Startup migration, not a user-initiated RPC -- nothing to
			// return the error to (this runs from the constructor).
			// Logged so a failure is diagnosable rather than silently
			// dropped (docs/goals/0025 item 1's fire-and-forget bucket);
			// worst case the migration simply re-runs identically on the
			// next launch, since legacyConnectorsKey itself is untouched.
			if err := c.persistHTTPRequests(); err != nil {
				slog.Error("failed to persist migrated legacy connectors", "error", err)
			}
		}
	} else {
		seeded := httprequest.BuiltIn()
		now := time.Now()
		for i := range seeded {
			seeded[i].CreatedAt, seeded[i].UpdatedAt = now, now
		}
		c.requests = seeded
	}
	c.reconcileBuiltInRequests()
	if raw, ok := c.store.Get(listsKey).(string); ok && raw != "" {
		var lists []list.List
		if err := json.Unmarshal([]byte(raw), &lists); err == nil {
			c.lists = lists
			c.migrateLegacyLists()
		}
	}
}
