//go:build postgres_integration

package dataplatform

import (
	"context"
	"testing"
	"time"
)

func TestSkillEvidencePlanesColdIdentityAndProfileAgainstPostgres(t *testing.T) {
	pool := freshSchema(t, testDSN(t))
	ctx := context.Background()
	base := time.Date(2026, 7, 26, 10, 0, 0, 0, time.UTC)
	if err := EnsureDimensions(ctx, pool, testDimensionRefs("src_skill_bridge")); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO components (component_id,kind,declared_name,source_scope)
		VALUES ('cmp_noop_skill','skill','kansoku-noop-skill','user')
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO component_versions (component_version_id,component_id,version,version_state)
		VALUES ('cv_noop_skill','cmp_noop_skill','1.0.0','observed')
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO component_installations (
			component_installation_id,component_version_id,agent_installation_id
		) VALUES ('ci_noop_skill','cv_noop_skill','ain_fixture')
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO inventory_snapshots (
			snapshot_id,adapter_id,adapter_version,agent_installation_id,
			observed_at,fingerprint,completeness
		) VALUES ('snap_noop_skill','fixture-agent','1.0.0','ain_fixture',$1,
			'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','complete')
	`, base); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO component_inventory_state (
			component_installation_id,inventory_node_id,enabled,first_seen_at,
			last_seen_at,last_snapshot_id
		) VALUES ('ci_noop_skill','node_noop_skill',true,$1,$1,'snap_noop_skill')
	`, base); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO component_observation_windows (
			observation_window_id,component_installation_id,source_instance_id,
			plane,window_start,window_end,completeness,idempotency_key
		) VALUES ('win_noop','ci_noop_skill','src_skill_bridge','availability',$1,$2,
			'complete','window-noop')
	`, base, base.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO component_file_tree_metadata (
			component_installation_id,inventory_snapshot_id,node_pseudonym,
			parent_pseudonym,entry_kind,depth,byte_count
		) VALUES
			('ci_noop_skill','snap_noop_skill','tree_root',NULL,'directory',0,NULL),
			('ci_noop_skill','snap_noop_skill','tree_skill','tree_root','file',1,280)
	`); err != nil {
		t.Fatal(err)
	}
	insertAssertion := func(id, kind, mode, resolution string, candidateCount int, componentID any) {
		t.Helper()
		if _, err := pool.Exec(ctx, `
			INSERT INTO component_assertions (
				assertion_id,component_installation_id,agent_installation_id,
				assertion_kind,mode,evidence_tier,confidence,source_instance_id,
				adapter_version,schema_version,observed_at,idempotency_key,
				identity_resolution,declared_identity_pseudonym,candidate_count
			) VALUES ($1,$2,'ain_fixture',$3,$4,'native',1,'src_skill_bridge',
				'1.0.0','fixture.skill/1',$5,$1,$6,'hmac-sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',$7)
		`, id, componentID, kind, mode, base.Add(10*time.Minute), resolution, candidateCount); err != nil {
			t.Fatal(err)
		}
	}
	insertAssertion("assert_exposed", "exposed", "not_observed", "exact", 1, "ci_noop_skill")
	insertAssertion("assert_invoked", "invoked", "explicit", "exact", 1, "ci_noop_skill")
	insertAssertion("assert_loaded", "loaded", "not_observed", "exact", 1, "ci_noop_skill")
	insertAssertion("assert_ambiguous", "child_activity", "not_observed", "ambiguous", 2, nil)

	response, err := SkillObservatory(ctx, pool, base, base.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Data) != 1 {
		t.Fatalf("skill rows=%d want 1", len(response.Data))
	}
	row := response.Data[0]
	if row.ExposedCount != 1 || row.InvokedCount != 1 || row.LoadedCount != 1 ||
		row.ColdState != "used" || row.OutcomeState != "unsupported" {
		t.Fatalf("unexpected planes: %+v", row)
	}
	if response.Population != (Population{Numerator: 0, Denominator: 1}) ||
		response.Exclusions["ambiguous_identity"] != 1 {
		t.Fatalf("population/exclusions mismatch: %+v %+v", response.Population, response.Exclusions)
	}
	profile, err := SkillProfile(ctx, pool, "ci_noop_skill", base, base.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(profile.Assertions) != 3 || len(profile.Sources) != 1 ||
		len(profile.FileTree) != 1 || profile.FileTree[0].FileCount != 1 {
		t.Fatalf("profile mismatch: %+v", profile)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO component_assertions (
			assertion_id,component_installation_id,agent_installation_id,
			assertion_kind,mode,outcome,evidence_tier,confidence,source_instance_id,
			adapter_version,schema_version,observed_at,idempotency_key,
			identity_resolution,declared_identity_pseudonym,candidate_count
		) VALUES ('invalid_outcome','ci_noop_skill','ain_fixture','outcome','not_observed',
			'succeeded','native',1,'src_skill_bridge','1.0.0','fixture.skill/1',$1,
			'invalid-outcome','exact',
			'hmac-sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb',1)
	`, base); err == nil {
		t.Fatal("outcome without terminal contract was accepted")
	}

	if _, err := pool.Exec(ctx, `
		UPDATE component_observation_windows SET completeness='partial'
		WHERE observation_window_id='win_noop'
	`); err != nil {
		t.Fatal(err)
	}
	partial, err := SkillObservatory(ctx, pool, base, base.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if partial.Data[0].ColdState != "not_observed" ||
		partial.Exclusions["partial_or_missing_exposure_window"] != 1 {
		t.Fatalf("source-loss semantics mismatch: %+v", partial)
	}
}
