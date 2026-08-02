//go:build postgres_integration

package dataplatform

import (
	"context"
	"testing"
	"time"

	"kansoku.local/kansoku/internal/adaptersdk"
)

// TestSkillColdEligibilityAcrossEveryExposurePlaneState exercises every branch
// of skill.cold_count/2 against a real database.
//
// The case that motivated the formula change is "unsupported plane, complete
// inventory": before it existed, an agent that publishes no model-visible
// skill set — Claude Code — reported every skill not_observed forever, which
// read as "we looked and saw nothing" when the truth was "there is no surface
// to look at". The case that keeps it honest is "unsupported plane, partial
// inventory": a mis-mounted host must fall out of the denominator rather than
// produce a confident cold count over a silently truncated scan.
//
// The NULL case is the compatibility proof: an installation with no plane
// declaration must behave exactly as it did under /1.
func TestSkillColdEligibilityAcrossEveryExposurePlaneState(t *testing.T) {
	pool := freshSchema(t, testDSN(t))
	ctx := context.Background()
	base := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	if err := EnsureDimensions(ctx, pool, testDimensionRefs("src_plane_support")); err != nil {
		t.Fatal(err)
	}

	// Each fixture is one skill on its own installation, so the four states
	// are observed independently in a single query result.
	type fixture struct {
		suffix          string
		installation    string
		planeState      string
		snapshotState   string
		exposureWindow  bool
		exposedAssert   bool
		invokedAssert   bool
		wantCold        string
		wantExposure    string
		wantInventory   string
		wantEligibility bool
	}
	fixtures := []fixture{{
		suffix: "native_exposed", installation: "ain_native",
		planeState: "native", snapshotState: "complete",
		exposureWindow: true, exposedAssert: true, invokedAssert: false,
		wantCold: "cold", wantExposure: "observed",
		wantInventory: "complete", wantEligibility: true,
	}, {
		suffix: "native_no_window", installation: "ain_native_gap",
		planeState: "native", snapshotState: "complete",
		exposureWindow: false, exposedAssert: false, invokedAssert: true,
		wantCold: "not_observed", wantExposure: "not_observed",
		wantInventory: "complete", wantEligibility: false,
	}, {
		suffix: "unsupported_complete", installation: "ain_unsupported",
		planeState: "unsupported", snapshotState: "complete",
		exposureWindow: false, exposedAssert: false, invokedAssert: true,
		wantCold: "used", wantExposure: "unsupported",
		wantInventory: "complete", wantEligibility: true,
	}, {
		suffix: "unsupported_partial", installation: "ain_unsupported_partial",
		planeState: "unsupported", snapshotState: "partial",
		exposureWindow: false, exposedAssert: false, invokedAssert: true,
		wantCold: "not_observed", wantExposure: "unsupported",
		wantInventory: "partial", wantEligibility: false,
	}, {
		suffix: "undeclared", installation: "ain_undeclared",
		planeState: "", snapshotState: "complete",
		exposureWindow: true, exposedAssert: true, invokedAssert: true,
		wantCold: "used", wantExposure: "observed",
		wantInventory: "complete", wantEligibility: true,
	}}

	for _, item := range fixtures {
		installation := item.installation
		if _, err := pool.Exec(ctx, `
			INSERT INTO agent_installations (agent_installation_id, device_id, agent_id)
			VALUES ($1,'dev_fixture','fixture-agent')
			ON CONFLICT (agent_installation_id) DO NOTHING
		`, installation); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO components (component_id,kind,declared_name,source_scope)
			VALUES ('cmp_'||$1,'skill','skill-'||$1,'user')
		`, item.suffix); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO component_versions (component_version_id,component_id,version,version_state)
			VALUES ('cv_'||$1,'cmp_'||$1,'1.0.0','observed')
		`, item.suffix); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO component_installations (
				component_installation_id,component_version_id,agent_installation_id
			) VALUES ('ci_'||$1,'cv_'||$1,$2)
		`, item.suffix, installation); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO inventory_snapshots (
				snapshot_id,adapter_id,adapter_version,agent_installation_id,
				observed_at,fingerprint,completeness
			) VALUES ('snap_'||$1,'fixture-agent','1.0.0',$2,$3,'ffffffffffffffffffffffffffffffff',$4)
		`, item.suffix, installation, base, item.snapshotState); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO component_inventory_state (
				component_installation_id,inventory_node_id,enabled,first_seen_at,
				last_seen_at,last_snapshot_id
			) VALUES ('ci_'||$1,'node_'||$1,true,$2,$2,'snap_'||$1)
		`, item.suffix, base); err != nil {
			t.Fatal(err)
		}
		if item.planeState != "" {
			if _, err := pool.Exec(ctx, `
				INSERT INTO agent_component_plane_support (
					agent_installation_id,component_kind,plane,state,reason,
					adapter_id,adapter_version,observed_at
				) VALUES ($1,'skill','exposed',$2,'fixture_reason','fixture-agent','1.0.0',$3)
				ON CONFLICT (agent_installation_id,component_kind,plane) DO UPDATE
				SET state=EXCLUDED.state
			`, installation, item.planeState, base); err != nil {
				t.Fatal(err)
			}
		}
		if item.exposureWindow {
			if _, err := pool.Exec(ctx, `
				INSERT INTO component_observation_windows (
					observation_window_id,component_installation_id,source_instance_id,
					plane,window_start,window_end,completeness,idempotency_key
				) VALUES ('win_'||$1,'ci_'||$1,'src_plane_support','availability',$2,$3,
					'complete','window-'||$1)
			`, item.suffix, base, base.Add(time.Hour)); err != nil {
				t.Fatal(err)
			}
		}
		insert := func(kind, id string) {
			t.Helper()
			if _, err := pool.Exec(ctx, `
				INSERT INTO component_assertions (
					assertion_id,component_installation_id,agent_installation_id,
					assertion_kind,mode,evidence_tier,confidence,source_instance_id,
					adapter_version,schema_version,observed_at,idempotency_key,
					identity_resolution,declared_identity_pseudonym,candidate_count,
					component_kind
				) VALUES ($1,'ci_'||$2,$3,$4,'not_observed','native',1,'src_plane_support',
					'1.0.0','fixture.skill/1',$5,$1,'exact',
					'hmac-sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc',1,'skill')
			`, id, item.suffix, installation, kind, base.Add(10*time.Minute)); err != nil {
				t.Fatal(err)
			}
		}
		if item.exposedAssert {
			insert("exposed", "assert_exposed_"+item.suffix)
		}
		if item.invokedAssert {
			insert("invoked", "assert_invoked_"+item.suffix)
		}
	}

	response, err := SkillObservatory(ctx, pool, base, base.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if response.FormulaVersion != FormulaVersionSkillObservatory2 {
		t.Fatalf("formula version=%s want %s", response.FormulaVersion, FormulaVersionSkillObservatory2)
	}
	byInstallation := map[string]SkillObservatoryRow{}
	for _, row := range response.Data {
		byInstallation[row.ComponentInstallationID] = row
	}
	var wantEligible int64
	for _, item := range fixtures {
		row, ok := byInstallation["ci_"+item.suffix]
		if !ok {
			t.Fatalf("%s: row missing", item.suffix)
		}
		if row.ColdState != item.wantCold {
			t.Fatalf("%s: cold_state=%s want %s", item.suffix, row.ColdState, item.wantCold)
		}
		if row.ExposureState != item.wantExposure {
			t.Fatalf("%s: exposure_state=%s want %s", item.suffix, row.ExposureState, item.wantExposure)
		}
		if row.InventoryCoverage != item.wantInventory {
			t.Fatalf("%s: inventory_coverage=%s want %s", item.suffix, row.InventoryCoverage, item.wantInventory)
		}
		if item.wantExposure == "unsupported" && row.ExposureReason == "" {
			t.Fatalf("%s: unsupported exposure carries no reason", item.suffix)
		}
		if item.wantEligibility {
			wantEligible++
		}
	}
	if response.Population.Denominator != wantEligible {
		t.Fatalf("denominator=%d want %d", response.Population.Denominator, wantEligible)
	}
	// The two exposure exclusions must partition the ineligible enabled rows,
	// so a row is never counted under both.
	sum := response.Exclusions["partial_or_missing_exposure_window"] +
		response.Exclusions["exposure_plane_unsupported_without_complete_inventory"]
	if sum != response.Counts.Enabled-response.Population.Denominator {
		t.Fatalf("exclusions %d do not partition enabled(%d)-eligible(%d)",
			sum, response.Counts.Enabled, response.Population.Denominator)
	}
	if response.Exclusions["partial_or_missing_exposure_window"] != 1 {
		t.Fatalf("supported-plane exclusion=%d want 1",
			response.Exclusions["partial_or_missing_exposure_window"])
	}
	if response.Exclusions["exposure_plane_unsupported_without_complete_inventory"] != 1 {
		t.Fatalf("unsupported-plane exclusion=%d want 1",
			response.Exclusions["exposure_plane_unsupported_without_complete_inventory"])
	}
}

// TestComponentPlaneSupportUpsertIsIdempotentAndReplaces proves the projection
// an adapter manifest writes each scan: repeating a scan changes nothing, and
// an adapter that changes its declaration replaces the previous claim rather
// than leaving two standing.
func TestComponentPlaneSupportUpsertIsIdempotentAndReplaces(t *testing.T) {
	pool := freshSchema(t, testDSN(t))
	ctx := context.Background()
	base := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	if err := EnsureDimensions(ctx, pool, testDimensionRefs("src_plane_upsert")); err != nil {
		t.Fatal(err)
	}
	manifest := planeSupportTestManifest("unsupported", "documents_no_model_visible_skill_set")
	for range 2 {
		if err := UpsertComponentPlaneSupport(ctx, pool, "ain_fixture", manifest, base); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := ComponentPlaneSupportFor(ctx, pool, "ain_fixture")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].State != "unsupported" {
		t.Fatalf("rows=%+v want one unsupported declaration", rows)
	}
	changed := planeSupportTestManifest("native", "now_reported_natively")
	if err := UpsertComponentPlaneSupport(ctx, pool, "ain_fixture", changed, base.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	rows, err = ComponentPlaneSupportFor(ctx, pool, "ain_fixture")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].State != "native" || rows[0].Reason != "now_reported_natively" {
		t.Fatalf("rows=%+v want the previous claim replaced", rows)
	}
}

func planeSupportTestManifest(state, reason string) adaptersdk.Manifest {
	return adaptersdk.Manifest{
		APIVersion: adaptersdk.AdapterAPIVersion, ID: "fixture-agent", Version: "1.0.0",
		Execution: adaptersdk.ExecutionBuiltin,
		ComponentPlaneSupport: []adaptersdk.ComponentPlaneSupport{{
			ComponentKind: "skill", Plane: adaptersdk.PlaneExposed,
			State: adaptersdk.PlaneSupportState(state), Reason: reason,
		}},
	}
}
