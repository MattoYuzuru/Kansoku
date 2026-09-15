package dataplatform

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"kansoku.local/kansoku/internal/adaptersdk"
)

// ComponentPlaneSupportRow is one persisted adapter declaration.
type ComponentPlaneSupportRow struct {
	AgentInstallationID string `json:"agent_installation_id"`
	ComponentKind       string `json:"component_kind"`
	Plane               string `json:"plane"`
	State               string `json:"state"`
	Reason              string `json:"reason"`
}

// UpsertComponentPlaneSupport records one installation's adapter-declared
// evidence plane support, taken verbatim from the registered adapter's own
// manifest. It is stored as data so every query that needs the distinction
// reads a row rather than branching on an agent name — the same reason a
// second agent with the same missing surface (Gemini, Cursor) will inherit the
// behaviour by declaring it, with no core change at all.
//
// The upsert replaces the row for each declared (kind, plane): an adapter that
// changes its declaration between releases must not leave the previous claim
// standing. A plane the manifest says nothing about is left alone rather than
// deleted — "undeclared" is not "unsupported", and downstream treats an absent
// row exactly as today's behaviour, which is what keeps fakeadapter, wayfinder
// and every future adapter working unchanged.
func UpsertComponentPlaneSupport(
	ctx context.Context,
	pool *pgxpool.Pool,
	agentInstallationID string,
	manifest adaptersdk.Manifest,
	observedAt time.Time,
) error {
	if pool == nil || agentInstallationID == "" || len(manifest.ComponentPlaneSupport) == 0 {
		return nil
	}
	for _, declaration := range manifest.ComponentPlaneSupport {
		if _, err := pool.Exec(ctx, `
			INSERT INTO agent_component_plane_support (
				agent_installation_id, component_kind, plane, state, reason,
				adapter_id, adapter_version, observed_at
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
			ON CONFLICT (agent_installation_id, component_kind, plane) DO UPDATE SET
				state = EXCLUDED.state,
				reason = EXCLUDED.reason,
				adapter_id = EXCLUDED.adapter_id,
				adapter_version = EXCLUDED.adapter_version,
				observed_at = GREATEST(
					agent_component_plane_support.observed_at, EXCLUDED.observed_at
				)
		`, agentInstallationID, declaration.ComponentKind, string(declaration.Plane),
			string(declaration.State), declaration.Reason, manifest.ID, manifest.Version,
			observedAt.UTC()); err != nil {
			return err
		}
	}
	return nil
}

// ComponentPlaneSupportFor returns every declaration recorded for one
// installation, ordered for stable output. Used by tests and by the system
// snapshot; the observatory queries join the table directly.
func ComponentPlaneSupportFor(
	ctx context.Context, pool *pgxpool.Pool, agentInstallationID string,
) ([]ComponentPlaneSupportRow, error) {
	rows, err := pool.Query(ctx, `
		SELECT agent_installation_id, component_kind, plane, state, reason
		FROM agent_component_plane_support
		WHERE agent_installation_id = $1
		ORDER BY component_kind, plane
	`, agentInstallationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	declarations := make([]ComponentPlaneSupportRow, 0)
	for rows.Next() {
		var row ComponentPlaneSupportRow
		if err := rows.Scan(
			&row.AgentInstallationID, &row.ComponentKind, &row.Plane, &row.State, &row.Reason,
		); err != nil {
			return nil, err
		}
		declarations = append(declarations, row)
	}
	return declarations, rows.Err()
}
