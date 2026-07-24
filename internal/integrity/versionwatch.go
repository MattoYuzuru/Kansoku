package integrity

import (
	"sort"

	"kansoku.local/kansoku/internal/adaptersdk"
)

// AdapterVersionSnapshot is adapter_id -> Manifest().Version for every
// registered adapter, taken at one point in time. This is the ONLY
// version-tracking mechanism this package uses: it reads
// adaptersdk.Registry.IDs()/Get()/Manifest() directly (per ADR 0011 "query
// this via the existing adaptersdk Registry/Manifest version fields -- do
// not add a second version-tracking mechanism") rather than caching or
// re-deriving adapter versions from anywhere else.
type AdapterVersionSnapshot map[string]string

// SnapshotAdapterVersions reads every registered adapter's manifest version
// from registry, matching drift-fingerprint-and-schema.yaml's
// adapter_version fingerprint kind ("internal/adaptersdk
// Registry.Get(adapter_id).Manifest() version field").
func SnapshotAdapterVersions(registry *adaptersdk.Registry) (AdapterVersionSnapshot, error) {
	snapshot := AdapterVersionSnapshot{}
	for _, id := range registry.IDs() {
		adapter, err := registry.Get(id)
		if err != nil {
			return nil, err
		}
		snapshot[id] = adapter.Manifest().Version
	}
	return snapshot, nil
}

// ChangedAdapterIDs compares two snapshots and returns, in sorted order,
// every adapter_id whose version changed or that was added/removed between
// previous and current. An empty or nil previous snapshot (e.g. process
// startup with no prior recorded snapshot) never reports a version change
// by itself -- there is nothing to compare against yet, matching the
// "gray is the honest default before any check has run" posture rather
// than treating first observation as drift.
func ChangedAdapterIDs(previous, current AdapterVersionSnapshot) []string {
	if len(previous) == 0 {
		return nil
	}
	changed := map[string]bool{}
	for id, version := range current {
		if prior, ok := previous[id]; !ok || prior != version {
			changed[id] = true
		}
	}
	for id := range previous {
		if _, ok := current[id]; !ok {
			changed[id] = true
		}
	}
	ids := make([]string, 0, len(changed))
	for id := range changed {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
