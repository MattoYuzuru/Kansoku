package adaptersdk

import (
	"context"
	"errors"
	"sort"
	"sync"

	"kansoku.local/kansoku/internal/privacy"
)

// SafeSourceRecord is the input to Normalize: it is exactly
// privacy.SafeRecord, the same typed sanitizer output every other Session
// 02+ boundary consumes. adaptersdk never defines a second sanitizer or a
// second raw-payload type; Normalize only ever receives what the sanitizer
// already allowlisted.
type SafeSourceRecord = privacy.SafeRecord

// CanonicalEvent is the adapter-facing name for one normalized fact. It is
// intentionally a placeholder alias here: the authoritative canonical event
// shape is internal/observability.Event (Session 03). An adapter's
// Normalize implementation is expected to produce that concrete type; this
// alias exists only so the Adapter interface signature in this package does
// not have to import internal/observability and create a cross-domain
// compile dependency for adapters that only need discovery/inventory.
type CanonicalEvent = privacy.SafeRecord

// Adapter is the exact interface every agent integration implements,
// whether builtin, external-process, wasm or container. Nothing in this
// package, the Registry, or any caller of the Registry ever type-switches
// or string-switches on which concrete Adapter is in use: all
// agent-specific behavior is reached only via the methods below, dispatched
// through the adapter's own registered AdapterID.
type Adapter interface {
	Manifest() Manifest
	Discover(ctx context.Context, host *HostView) ([]InstallationCandidate, error)
	Inventory(ctx context.Context, target Installation) (InventorySnapshot, error)
	PlanConfiguration(ctx context.Context, target Installation, capability CapabilityID) (ChangePlan, error)
	SourceSchemas() []privacy.SourceSchema
	Normalize(ctx context.Context, source SafeSourceRecord) ([]CanonicalEvent, error)
	Reconcile(ctx context.Context, scope ReconcileScope, previous, current InventorySnapshot) ReconcileResult
	Audit(ctx context.Context, target Installation, mode AuditMode) []CheckResult
}

// Registry holds every registered Adapter by its manifest ID. Lookup and
// iteration are the only operations the core exposes; there is no
// agent-name conditional anywhere in this type. Adding a new agent means
// calling Register with a new Adapter value, not editing a switch
// statement here.
type Registry struct {
	mu       sync.RWMutex
	adapters map[string]Adapter
}

// NewRegistry returns an empty adapter registry.
func NewRegistry() *Registry {
	return &Registry{adapters: map[string]Adapter{}}
}

// ErrDuplicateAdapterID is returned by Register when an adapter with the
// same manifest ID is already registered.
var ErrDuplicateAdapterID = errors.New("duplicate_adapter_id")

// ErrAdapterNotFound is returned by Get/capability lookups when no adapter
// with the requested ID is registered.
var ErrAdapterNotFound = errors.New("adapter_not_found")

// Register validates the adapter's manifest and adds it to the registry
// under its manifest ID. It never inspects the ID string for a known agent
// brand; any well-formed manifest ID is accepted equally.
func (r *Registry) Register(adapter Adapter) error {
	manifest := adapter.Manifest()
	if err := validateManifestShape(manifest); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.adapters[manifest.ID]; exists {
		return ErrDuplicateAdapterID
	}
	r.adapters[manifest.ID] = adapter
	return nil
}

// Get returns the adapter registered under id.
func (r *Registry) Get(id string) (Adapter, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	adapter, ok := r.adapters[id]
	if !ok {
		return nil, ErrAdapterNotFound
	}
	return adapter, nil
}

// IDs returns every registered adapter ID in stable sorted order.
func (r *Registry) IDs() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids := make([]string, 0, len(r.adapters))
	for id := range r.adapters {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// CapabilityMatrix runs Manifest() across every registered adapter and
// returns, for each adapter ID, the set of capability IDs its manifest
// declares. This is the same data kansoku doctor would render; the routing
// here is entirely capability-ID keyed, with the adapter ID only used as a
// grouping label -- never as a branch condition.
func (r *Registry) CapabilityMatrix() map[string][]CapabilityID {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make(map[string][]CapabilityID, len(r.adapters))
	for id, adapter := range r.adapters {
		manifest := adapter.Manifest()
		capabilities := make([]CapabilityID, 0, len(manifest.Capabilities))
		for capability := range manifest.Capabilities {
			capabilities = append(capabilities, capability)
		}
		sort.Slice(capabilities, func(i, j int) bool { return capabilities[i] < capabilities[j] })
		result[id] = capabilities
	}
	return result
}
