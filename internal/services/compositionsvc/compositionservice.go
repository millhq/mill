// Package compositionsvc is the Wails-facing layer over
// internal/domain/composition: persistence, ID/versioning, and
// import/export for user-authored workflows (nodes, edges, attributes).
// Domain validation and node-type execution semantics stay in the
// domain package (.claude/rules/backend.md's purity rule); this
// package owns only the state and settings-store persistence a
// stateless domain package can't hold, plus the Syncer seam that tells
// TriggerService when the workflow set changes.
package compositionsvc

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/alicoding/mill/internal/adapters/settings"
	"github.com/alicoding/mill/internal/domain/composition"
	"github.com/alicoding/mill/internal/services/dataevent"
	"github.com/alicoding/mill/internal/services/seeding"
)

// workflowsKey is the single settings-store key holding every
// user-composed workflow as one JSON blob -- same shape as
// triggerHotkeyBindingsKey (triggerservice.go): one atomic read/write,
// sharing the same settings.json file rather than a second store/file.
// This is persistence for a workflow's authored *definition* (which
// nodes/edges, what config) -- a separate, already-settled concern from
// SPEC.md §7's still-open question of persisting/resuming a *running*
// workflow's execution state, which this does not touch or presuppose.
//
// Versioned ("-v2") because the persisted shape just changed (Steps ->
// Nodes+Edges, see composition.go's canvas migration): restore() already
// silently discards on unmarshal failure, so keeping the old key would
// mean old-shape data gets actively read and dropped on next launch.
// Renaming the key instead orphans it harmlessly. This is single-
// maintainer prototype data -- a real migration isn't warranted.
const workflowsKey = "composition-workflows-v2"

// Syncer is the seam CompositionService uses to tell TriggerService
// (triggerservice.go) "the workflow set changed, re-register whatever
// needs it" -- an interface, not a *TriggerService field directly,
// purely so this file doesn't have to know TriggerService's own
// construction order (it's wired in from main.go via SetSyncer once both
// services exist; TriggerService's constructor needs a *CompositionService
// to call RunWorkflow, so the two can't be constructed from a single
// mutual constructor call).
type Syncer interface {
	Sync(workflows []composition.Workflow)
}

// CompositionService is the Wails-facing layer over the composition
// domain package -- it holds no domain logic of its own (that's
// internal/domain/composition), only the state and persistence a
// stateless package can't own: user-composed workflows, mirroring
// TriggerService's own shape (triggerservice.go) rather than inventing a
// new one. See docs/SPEC.md §3's `UX: PROTOTYPE` entry for what this is
// testing.
type CompositionService struct {
	mu        sync.Mutex
	store     settings.Store
	user      []composition.Workflow
	syncer    Syncer
	onDeleted func(id string)
	// recordUndo journals a workflow delete into the app's ONE
	// actor-scoped undo journal (ADR-0044, goal 0404 S1) -- nil until
	// WireUndoJournal runs (compositionservice_undo.go), which every
	// test and headless build simply never calls.
	recordUndo        undoRecorder
	aiProviderSamples *aiProviderSampleCoordinator
}

func NewCompositionService(store settings.Store) *CompositionService {
	c := &CompositionService{store: store, aiProviderSamples: newAIProviderSampleCoordinator(nil)}
	c.restore()
	c.migratePublish()
	return c
}

// SetSyncer wires the syncer notified after every workflow mutation.
// Called once from main.go after both services exist -- see the Syncer
// doc comment above for why this can't just be a constructor parameter.
//
// wails:ignore -- Go-internal wiring only.
//
//wails:ignore
func (c *CompositionService) SetSyncer(s Syncer) {
	c.syncer = s
}

// SetWorkflowDeleted wires the hook DeleteWorkflow fires after a
// successful delete (docs/goals/0250): derived per-workflow state
// held by OTHER services (a trigger-hotkey binding) must be released
// when its workflow goes away, or it leaks in settings keyed by a
// dead id. Same injected-func seam as SetSyncer -- compositionsvc
// never imports the owning service.
//
//wails:ignore
func (c *CompositionService) SetWorkflowDeleted(fn func(id string)) {
	c.onDeleted = fn
}

// notifySyncer re-registers every workflow's trigger listener after a
// mutation -- a no-op until SetSyncer has run (defensive, not expected
// to matter: main.go wires it immediately after construction, before any
// real request reaches this service).
func (c *CompositionService) notifySyncer() {
	if c.syncer != nil {
		c.syncer.Sync(c.Workflows())
	}
}

func (c *CompositionService) NodeTypes() []composition.NodeType {
	return composition.NodeTypes()
}

// ValidateDraft runs composition.ValidateGraph against not-yet-saved
// canvas state -- the editor's own authoring-validation surface
// (docs/adr/0028), called on a debounce after every nodes/edges/config
// change so the toolbar badge and issues panel show every outstanding
// problem (warnings included), not just the first thing a real Save
// attempt would eventually reject on. Resolves node defaults first
// (deriving Kind server-side, same as Create/Update) so a still-
// drafting node's derived Kind can't drift from what a real save would
// compute; an unresolvable node type (e.g. mid node-type-swap, before
// the draft would ever be trusted for a real save) surfaces as a single
// Error issue instead of a raw RPC failure, so the panel always has
// something coherent to render.
func (c *CompositionService) ValidateDraft(nodes []composition.Node, edges []composition.Edge, attrs []composition.AttributeDef) []composition.Issue {
	resolved, err := composition.ResolveNodeDefaults(nodes)
	if err != nil {
		return []composition.Issue{{Severity: composition.SeverityError, Message: err.Error()}}
	}
	return composition.ValidateGraph(resolved, edges, attrs)
}

// CapabilityMap exposes docs/SPEC.md §3.3's capability map as real Go
// data, not parsed docs -- composition's own sub-capabilities (Trigger,
// Decision, Parallel Steps, ...), one level down from the page-level
// CapabilitiesService.List(). The in-app Spec view that used to render
// this was retired; docs/SPEC.md §3.3 remains the source of truth.
func (c *CompositionService) CapabilityMap() []composition.MapEntry {
	return composition.CapabilityMap()
}

// Workflows returns every workflow -- seeded examples and user-composed
// ones alike, all ordinary entries in c.user (see restore's first-run
// seeding below). No BuiltInWorkflows() call here: once seeded, they're
// just data, the same industry pattern confirmed in docs/SPEC.md §2.2's
// Update note (a Zapier template "operates independently... you can edit
// it like any other Zap" the moment it exists) -- not a protected
// specimen re-appended on every read.
func (c *CompositionService) Workflows() []composition.Workflow {
	c.mu.Lock()
	defer c.mu.Unlock()

	out := make([]composition.Workflow, len(c.user))
	copy(out, c.user)
	return out
}

// CreateWorkflow composes and configures a new workflow in one step, per
// SPEC.md §3: ResolveNodeDefaults validates every node's type and fills
// in any missing config with that field's default, so the stored
// workflow is never partially configured. The graph shape itself (does
// it form one valid chain?) isn't re-checked here -- ExecuteWorkflow
// validates that at run time, and the canvas is separately designed to
// prevent drawing an invalid graph in the first place.
func (c *CompositionService) CreateWorkflow(label, description string, nodes []composition.Node, edges []composition.Edge) (composition.Workflow, error) {
	return c.createWorkflowWithID(newWorkflowID(label), label, description, nodes, edges)
}

// createWorkflowWithID is CreateWorkflow's own logic, parameterized on
// the new workflow's id -- the seam ImportWorkflow uses to preserve a
// caller-supplied id (ADR-0036 decision 3: an id present but unknown
// locally creates a new workflow AT that id, not a freshly minted one,
// so a two-machine bridge round trip can write back by id).
func (c *CompositionService) createWorkflowWithID(id, label, description string, nodes []composition.Node, edges []composition.Edge) (composition.Workflow, error) {
	if strings.TrimSpace(label) == "" {
		return composition.Workflow{}, fmt.Errorf("a workflow needs a label")
	}
	if len(nodes) == 0 {
		return composition.Workflow{}, fmt.Errorf("a workflow needs at least one step")
	}

	resolved, err := composition.ResolveNodeDefaults(nodes)
	if err != nil {
		return composition.Workflow{}, err
	}
	if err := composition.ValidateGraphStrict(resolved, edges, nil); err != nil {
		return composition.Workflow{}, err
	}

	now := time.Now()
	wf := composition.Workflow{
		ID:          id,
		Label:       label,
		Description: description,
		Nodes:       resolved,
		Edges:       edges,
		BuiltIn:     false,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	c.mu.Lock()
	c.user = append(c.user, wf)
	c.mu.Unlock()

	if err := c.persist(); err != nil {
		// Persist failed -- don't leave a phantom-saved workflow in
		// memory that a restart would silently drop (docs/goals/0025
		// item 2: memory and store must not diverge). Remove by ID
		// rather than assuming it's still the last element -- a
		// concurrent Create could have appended after us before we
		// re-acquire the lock.
		c.mu.Lock()
		c.removeByIDLocked(wf.ID)
		c.mu.Unlock()
		return composition.Workflow{}, fmt.Errorf("save workflow: %w", err)
	}
	c.notifySyncer()
	// Live-sync (goal 0017): a direct UI create must reach every other
	// open surface exactly like an MCP-authored one already does
	// (docs/adr/0025) -- ImportWorkflow delegates here, so this single
	// call covers both entry points.
	dataevent.Emit("workflow", wf.ID)
	return wf, nil
}

// removeByIDLocked removes the workflow with id from c.user, if present.
// Caller must hold c.mu. Shared by CreateWorkflow's persist-failure
// rollback and DeleteWorkflow's own removal.
func (c *CompositionService) removeByIDLocked(id string) {
	for i, wf := range c.user {
		if wf.ID == id {
			c.user = append(c.user[:i], c.user[i+1:]...)
			return
		}
	}
}

// UpdateWorkflow replaces an existing user-composed workflow's nodes/
// edges (and label/description) in place, keeping its ID stable -- so
// re-opening a saved workflow on the canvas and saving edits updates it
// rather than creating a duplicate. Every workflow (seeded or
// user-composed) lives in c.user (see Workflows' doc comment), so this
// works uniformly on both -- no built-in special case.
func (c *CompositionService) UpdateWorkflow(id, label, description string, nodes []composition.Node, edges []composition.Edge) (composition.Workflow, error) {
	if strings.TrimSpace(label) == "" {
		return composition.Workflow{}, fmt.Errorf("a workflow needs a label")
	}
	if len(nodes) == 0 {
		return composition.Workflow{}, fmt.Errorf("a workflow needs at least one step")
	}

	resolved, err := composition.ResolveNodeDefaults(nodes)
	if err != nil {
		return composition.Workflow{}, err
	}

	c.mu.Lock()
	idx := -1
	for i, wf := range c.user {
		if wf.ID == id {
			idx = i
			break
		}
	}
	if idx == -1 {
		c.mu.Unlock()
		return composition.Workflow{}, fmt.Errorf("no workflow with id %q", id)
	}

	if err := composition.ValidateGraphStrict(resolved, edges, c.user[idx].Attributes); err != nil {
		c.mu.Unlock()
		return composition.Workflow{}, err
	}

	wf := composition.Workflow{
		ID:          id,
		Label:       label,
		Description: description,
		Nodes:       resolved,
		Edges:       edges,
		Attributes:  c.user[idx].Attributes,
		// Carried forward, not reset to false: BuiltIn is purely
		// informational now (docs/SPEC.md §2.2's Update note) -- editing
		// a seeded example doesn't stop it having started as one.
		BuiltIn: c.user[idx].BuiltIn,
		// Lifecycle state survives a draft save (docs/adr/0021): Save
		// edits the draft head only -- it must never drop the version
		// history, move the live pointer, or re-enable a disabled
		// workflow.
		Disabled:         c.user[idx].Disabled,
		PublishedVersion: c.user[idx].PublishedVersion,
		Versions:         c.user[idx].Versions,
		// CreatedAt is preserved from storage, never trusted from the
		// wire (there's no wire CreatedAt here at all, but the
		// principle is the same as every other Update* path); UpdatedAt
		// always advances to now on a real save.
		CreatedAt: c.user[idx].CreatedAt,
		UpdatedAt: time.Now(),
		// Modified latch (docs/goals/0037 item 2): a real content edit
		// reaching a built-in-origin workflow through this RPC (also
		// the MCP update_workflow path, via UpdateWorkflowFromExport)
		// permanently protects it from reconcile's upgrade path.
		Seed: c.user[idx].Seed.Touch(),
		// Carried forward like Disabled/Versions: a draft save edits
		// the graph, never the offer declaration (SetWorkflowOffer is
		// its one mutation path besides import).
		OfferOnRequestID: c.user[idx].OfferOnRequestID,
	}
	previous := c.user[idx]
	c.user[idx] = wf
	c.mu.Unlock()

	if err := c.persist(); err != nil {
		// Roll back to the pre-update value -- see CreateWorkflow's own
		// rollback comment for why (docs/goals/0025 item 2).
		c.mu.Lock()
		c.restoreByIDLocked(id, previous)
		c.mu.Unlock()
		return composition.Workflow{}, fmt.Errorf("save workflow: %w", err)
	}
	c.notifySyncer()
	dataevent.Emit("workflow", wf.ID) // goal 0017: live-sync every open surface
	return wf, nil
}

// restoreByIDLocked overwrites the workflow with id back to prev, if
// still present -- caller must hold c.mu. Shared rollback helper for
// UpdateWorkflow/UpdateAttributes's persist-failure paths.
func (c *CompositionService) restoreByIDLocked(id string, prev composition.Workflow) {
	for i, wf := range c.user {
		if wf.ID == id {
			c.user[i] = prev
			return
		}
	}
}

// UpdateAttributes replaces a workflow's declared Attributes schema in
// place -- the delegate ConfigureService (configureservice.go) calls for
// its Attributes CRUD, per SPEC.md §3.5's "Configure-authored but
// workflow-scoped" cardinality (Attributes aren't their own top-level
// entity the way an HTTPRequest/List is, they're metadata on a Workflow).
// Re-validates the existing Nodes/Edges against the *new* schema before
// accepting it: a Decision edge referencing a field this change removes
// or retypes must be caught here, not left to silently break the next
// time that workflow runs.
func (c *CompositionService) UpdateAttributes(workflowID string, attrs []composition.AttributeDef) (composition.Workflow, error) {
	c.mu.Lock()
	idx := -1
	for i, wf := range c.user {
		if wf.ID == workflowID {
			idx = i
			break
		}
	}
	if idx == -1 {
		c.mu.Unlock()
		return composition.Workflow{}, fmt.Errorf("no workflow with id %q", workflowID)
	}

	if err := composition.ValidateGraphStrict(c.user[idx].Nodes, c.user[idx].Edges, attrs); err != nil {
		c.mu.Unlock()
		return composition.Workflow{}, err
	}

	previous := c.user[idx]
	c.user[idx].Attributes = attrs
	c.user[idx].UpdatedAt = time.Now()
	// Modified latch (docs/goals/0037 item 2) -- same reasoning as
	// UpdateWorkflow's own Seed.Touch() call above.
	c.user[idx].Seed = c.user[idx].Seed.Touch()
	wf := c.user[idx]
	c.mu.Unlock()

	if err := c.persist(); err != nil {
		c.mu.Lock()
		c.restoreByIDLocked(workflowID, previous)
		c.mu.Unlock()
		return composition.Workflow{}, fmt.Errorf("save workflow attributes: %w", err)
	}
	dataevent.Emit("workflow", wf.ID) // goal 0017: live-sync every open surface
	return wf, nil
}

func (c *CompositionService) persist() error {
	c.mu.Lock()
	user := make([]composition.Workflow, len(c.user))
	copy(user, c.user)
	c.mu.Unlock()

	data, err := json.Marshal(user)
	if err != nil {
		return fmt.Errorf("marshal workflows: %w", err)
	}
	if err := c.store.Set(workflowsKey, string(data)); err != nil {
		return fmt.Errorf("persist workflows: %w", err)
	}
	return nil
}

// restore loads persisted workflows, or -- on a genuinely fresh install,
// nothing ever persisted -- seeds c.user with the two example workflows
// composition.BuiltInWorkflows() defines. Seeded, not eagerly persisted:
// if the app closes before any real edit happens, re-seeding identically
// next launch is harmless (nothing was ever changed to lose); the moment
// any real mutation occurs (including deleting a seed), persist() below
// makes it real, and this early-return path never fires again. This is
// the one place BuiltInWorkflows() is still called -- see Workflows'
// doc comment for why every other read goes through c.user alone.
func (c *CompositionService) restore() {
	raw, ok := c.store.Get(workflowsKey).(string)
	if !ok || raw == "" {
		seeded := composition.BuiltInWorkflows()
		now := time.Now()
		for i := range seeded {
			seeded[i].CreatedAt = now
			seeded[i].UpdatedAt = now
		}
		c.mu.Lock()
		c.user = seeded
		c.mu.Unlock()
		return
	}
	var user []composition.Workflow
	if err := json.Unmarshal([]byte(raw), &user); err != nil {
		return
	}
	c.mu.Lock()
	c.user = user
	c.mu.Unlock()
	// reconcileBuiltIns (compositionservice_seedlifecycle.go,
	// docs/goals/0037) supersedes the old insert-only topUpBuiltIns:
	// insert/upgrade/leave-alone/skip per golden, not just insert.
	c.reconcileBuiltIns()
}

// newWorkflowID derives a readable, collision-resistant ID from the
// workflow's label (e.g. "My Workflow" -> "my-workflow-a1b2c3") -- see
// seeding.NewSlugID for the shared shape.
func newWorkflowID(label string) string {
	return seeding.NewSlugID(label, "workflow")
}
