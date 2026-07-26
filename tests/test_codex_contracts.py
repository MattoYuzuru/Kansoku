from __future__ import annotations

import copy
import unittest

from scripts import validate_codex


class Session06CodexContractTests(unittest.TestCase):
    def assert_has(self, errors: list[str], fragment: str) -> None:
        self.assertTrue(any(fragment in error for error in errors), errors)

    def test_contracts_code_dependencies_and_fixtures_validate(self) -> None:
        self.assertEqual([], validate_codex.validate())

    def test_each_semantic_registry_is_policy_locked(self) -> None:
        base = validate_codex.registries()
        for path in sorted(base):
            mutated = copy.deepcopy(base)
            mutated[path]["contract_version"] = "99.0.0"
            self.assert_has(validate_codex.validate(mutated, include_code=False), "semantic digest changed")

    def test_policy_versions_are_contiguous_and_trusted_history_is_append_only(self) -> None:
        base = validate_codex.registries()
        current = validate_codex.load(validate_codex.LOCK_PATH)
        changed = copy.deepcopy(base)
        changed["contracts/codex/manifest.yaml"]["contract_version"] = "99.0.0"
        transitioned = copy.deepcopy(current)
        transitioned["locks"].append({
            "policy_version": "codex.manifest/3",
            "registry": "contracts/codex/manifest.yaml",
            "semantic_sha256": validate_codex.semantic_sha256(changed["contracts/codex/manifest.yaml"]),
        })
        self.assertEqual([], validate_codex.validate(changed, transitioned, include_code=False, historical=current))

        reordered = copy.deepcopy(transitioned)
        reordered["locks"][0], reordered["locks"][1] = reordered["locks"][1], reordered["locks"][0]
        self.assert_has(validate_codex.validate(changed, reordered, include_code=False, historical=current), "append-only trusted prefix")

        skipped = copy.deepcopy(current)
        skipped["locks"].append({
            "policy_version": "codex.manifest/4",
            "registry": "contracts/codex/manifest.yaml",
            "semantic_sha256": validate_codex.semantic_sha256(changed["contracts/codex/manifest.yaml"]),
        })
        self.assert_has(validate_codex.validate(changed, skipped, include_code=False, historical=current), "start at 1 and remain contiguous")

    def _coherent(self, changed_registries: dict, mutated_file: str) -> tuple[dict, dict]:
        locks = validate_codex.load(validate_codex.LOCK_PATH)
        coherent = copy.deepcopy(locks)
        for item in coherent["locks"]:
            item["semantic_sha256"] = validate_codex.semantic_sha256(changed_registries[item["registry"]])
        return changed_registries, coherent

    def test_coherent_lock_mutation_cannot_remove_never_persist_raw_prompt_invariant(self) -> None:
        base = validate_codex.registries()
        changed = copy.deepcopy(base)
        changed["contracts/codex/hooks-and-otel.yaml"]["hook_source"]["helper_contract"]["prompt_feature_computation"] = (
            "prompt_features_are_computed_and_the_raw_prompt_may_also_be_forwarded_for_debugging"
        )
        changed, coherent = self._coherent(changed, "contracts/codex/hooks-and-otel.yaml")
        self.assert_has(
            validate_codex.validate(changed, coherent, include_code=False),
            "raw prompt text is never written to disk or sent",
        )

    def test_coherent_lock_mutation_cannot_remove_hook_trust_bypass_prohibition(self) -> None:
        base = validate_codex.registries()
        changed = copy.deepcopy(base)
        changed["contracts/codex/hooks-and-otel.yaml"]["hook_source"]["trust_and_enabled_state"]["bypass_forbidden"] = (
            "kansoku_may_silently_repair_an_untrusted_or_disabled_hook_if_convenient"
        )
        changed, coherent = self._coherent(changed, "contracts/codex/hooks-and-otel.yaml")
        self.assert_has(
            validate_codex.validate(changed, coherent, include_code=False),
            "hook trust/enabled-state bypass_forbidden text weakened",
        )

    def test_coherent_lock_mutation_cannot_remove_native_exact_activation_prohibition(self) -> None:
        base = validate_codex.registries()
        changed = copy.deepcopy(base)
        changed["contracts/codex/skill-evidence-and-reconciliation.yaml"]["skill_evidence_model"]["native_exact_activation_prohibition"] = (
            "reconstructed_or_inferred_tier_evidence_may_be_shown_as_a_native_exact_activation_when_convenient"
        )
        changed, coherent = self._coherent(changed, "contracts/codex/skill-evidence-and-reconciliation.yaml")
        self.assert_has(
            validate_codex.validate(changed, coherent, include_code=False),
            "native exact activation prohibition text weakened",
        )

    def test_coherent_lock_mutation_cannot_remove_ambiguous_ownership_rule(self) -> None:
        base = validate_codex.registries()
        changed = copy.deepcopy(base)
        changed["contracts/codex/skill-evidence-and-reconciliation.yaml"]["skill_evidence_model"]["ambiguous_ownership_rule"] = (
            "an_ambiguous_helper_or_mcp_call_may_be_converted_to_component.invoked_when_only_one_candidate_seems_likely"
        )
        changed, coherent = self._coherent(changed, "contracts/codex/skill-evidence-and-reconciliation.yaml")
        self.assert_has(
            validate_codex.validate(changed, coherent, include_code=False),
            "ambiguous ownership rule weakened",
        )

    def test_coherent_lock_mutation_cannot_remove_missing_source_degradation_rule(self) -> None:
        base = validate_codex.registries()
        changed = copy.deepcopy(base)
        changed["contracts/codex/skill-evidence-and-reconciliation.yaml"]["reconciliation"]["missing_source_rule"] = (
            "missing_one_expected_source_reports_zero_usage_for_the_whole_session_for_simplicity"
        )
        changed, coherent = self._coherent(changed, "contracts/codex/skill-evidence-and-reconciliation.yaml")
        self.assert_has(
            validate_codex.validate(changed, coherent, include_code=False),
            "missing-source reconciliation rule must degrade only that source",
        )

    def test_coherent_lock_mutation_cannot_remove_exit_gate_no_inferred_promotion(self) -> None:
        base = validate_codex.registries()
        changed = copy.deepcopy(base)
        changed["contracts/codex/skill-evidence-and-reconciliation.yaml"]["exit_gate"]["no_inferred_promoted_to_native"] = (
            "inferred_tier_evidence_may_be_promoted_to_native_when_confidence_is_high"
        )
        changed, coherent = self._coherent(changed, "contracts/codex/skill-evidence-and-reconciliation.yaml")
        self.assert_has(
            validate_codex.validate(changed, coherent, include_code=False),
            "exit gate no-inferred-promoted-to-native guarantee weakened",
        )

    def test_coherent_lock_mutation_cannot_drop_independent_source_degradation(self) -> None:
        base = validate_codex.registries()
        changed = copy.deepcopy(base)
        changed["contracts/codex/hooks-and-otel.yaml"]["independent_capability_degradation"] = (
            "disabling_codex.hook_also_zeroes_out_codex.otel_evidence_for_simplicity"
        )
        changed, coherent = self._coherent(changed, "contracts/codex/hooks-and-otel.yaml")
        self.assert_has(
            validate_codex.validate(changed, coherent, include_code=False),
            "hook/OTel independent capability degradation guarantee weakened",
        )

    def test_coherent_lock_mutation_cannot_widen_network_grade(self) -> None:
        base = validate_codex.registries()
        changed = copy.deepcopy(base)
        changed["contracts/codex/manifest.yaml"]["network_grade"] = "unrestricted"
        changed, coherent = self._coherent(changed, "contracts/codex/manifest.yaml")
        self.assert_has(
            validate_codex.validate(changed, coherent, include_code=False),
            "network grade must remain loopback_only",
        )

    def test_coherent_lock_mutation_cannot_permit_code_execution_in_parsing(self) -> None:
        base = validate_codex.registries()
        changed = copy.deepcopy(base)
        changed["contracts/codex/manifest.yaml"]["reused_parse_limits"]["code_execution"] = "permitted_for_trusted_hook_helper_binaries"
        changed, coherent = self._coherent(changed, "contracts/codex/manifest.yaml")
        self.assert_has(
            validate_codex.validate(changed, coherent, include_code=False),
            "manifest parsing must explicitly forbid code execution",
        )

    def test_coherent_lock_mutation_cannot_allow_speculative_home_scan(self) -> None:
        base = validate_codex.registries()
        changed = copy.deepcopy(base)
        changed["contracts/codex/manifest.yaml"]["installation_discovery"]["never_speculative_home_scan"] = (
            "the_adapter_may_scan_the_entire_home_directory_when_CODEX_HOME_is_unset"
        )
        changed, coherent = self._coherent(changed, "contracts/codex/manifest.yaml")
        self.assert_has(
            validate_codex.validate(changed, coherent, include_code=False),
            "installation discovery must resolve CODEX_HOME",
        )

    def test_coherent_lock_mutation_cannot_redefine_otel_installer_target(self) -> None:
        base = validate_codex.registries()
        changed = copy.deepcopy(base)
        changed["contracts/codex/manifest.yaml"]["installer_target_reuse"]["otel"] = (
            "a_new_codex_specific_otel_target_is_declared_here_instead_of_reusing_codex.user_otel"
        )
        changed, coherent = self._coherent(changed, "contracts/codex/manifest.yaml")
        self.assert_has(
            validate_codex.validate(changed, coherent, include_code=False),
            "manifest must reuse contracts/privacy/installer.yaml's existing codex.user_otel target verbatim",
        )

    def test_coherent_lock_mutation_cannot_introduce_a_parallel_ingress_route(self) -> None:
        base = validate_codex.registries()
        changed = copy.deepcopy(base)
        changed["contracts/codex/hooks-and-otel.yaml"]["hook_source"]["route"] = "/v1/codex/hooks/{event}"
        changed, coherent = self._coherent(changed, "contracts/codex/hooks-and-otel.yaml")
        self.assert_has(
            validate_codex.validate(changed, coherent, include_code=False),
            "codex.hook route must remain /v1/hooks/codex/{event}",
        )

    def test_coherent_lock_mutation_cannot_permit_project_local_default_scope(self) -> None:
        base = validate_codex.registries()
        changed = copy.deepcopy(base)
        changed["contracts/codex/manifest.yaml"]["installer_target_reuse"]["project_local_scope"] = (
            "project_local_codex_config_is_the_default_scope_for_convenience"
        )
        changed, coherent = self._coherent(changed, "contracts/codex/manifest.yaml")
        self.assert_has(
            validate_codex.validate(changed, coherent, include_code=False),
            "project-local Codex config scope must never be selected by default",
        )

    def test_coherent_lock_mutation_cannot_invent_a_new_capability_id(self) -> None:
        base = validate_codex.registries()
        changed = copy.deepcopy(base)
        changed["contracts/codex/manifest.yaml"]["capability_ids"] = changed["contracts/codex/manifest.yaml"]["capability_ids"] + ["codex.special_capability"]
        changed, coherent = self._coherent(changed, "contracts/codex/manifest.yaml")
        self.assert_has(
            validate_codex.validate(changed, coherent, include_code=False),
            "codex manifest capability_ids must reuse adapter-sdk's closed capability id list verbatim",
        )

    def test_coherent_lock_mutation_cannot_widen_cache_separation_rule(self) -> None:
        base = validate_codex.registries()
        changed = copy.deepcopy(base)
        changed["contracts/codex/rollout-and-inventory.yaml"]["inventory_source"]["cache_rule"] = (
            "cache_packages_may_be_considered_enabled_if_recently_installed"
        )
        changed, coherent = self._coherent(changed, "contracts/codex/rollout-and-inventory.yaml")
        self.assert_has(
            validate_codex.validate(changed, coherent, include_code=False),
            "codex.inventory cache separation rule weakened",
        )

    def test_coherent_lock_mutation_cannot_allow_recursive_repository_scan(self) -> None:
        base = validate_codex.registries()
        changed = copy.deepcopy(base)
        changed["contracts/codex/rollout-and-inventory.yaml"]["inventory_source"]["repository_scan_bound"] = (
            "repository_roots_may_be_discovered_via_a_speculative_recursive_filesystem_walk"
        )
        changed, coherent = self._coherent(changed, "contracts/codex/rollout-and-inventory.yaml")
        self.assert_has(
            validate_codex.validate(changed, coherent, include_code=False),
            "codex.inventory repository scan must never be a speculative recursive filesystem walk",
        )

    def test_coherent_lock_mutation_cannot_make_rollout_replay_non_idempotent(self) -> None:
        base = validate_codex.registries()
        changed = copy.deepcopy(base)
        changed["contracts/codex/rollout-and-inventory.yaml"]["rollout_source"]["replay_and_crash"]["idempotency"] = (
            "replaying_the_same_byte_range_twice_may_yield_a_duplicate_fact"
        )
        changed, coherent = self._coherent(changed, "contracts/codex/rollout-and-inventory.yaml")
        self.assert_has(
            validate_codex.validate(changed, coherent, include_code=False),
            "rollout replay idempotency guarantee weakened",
        )

    def test_coherent_lock_mutation_cannot_allow_durable_raw_bytes_on_quarantine(self) -> None:
        base = validate_codex.registries()
        changed = copy.deepcopy(base)
        changed["contracts/codex/rollout-and-inventory.yaml"]["rollout_source"]["corrupt_or_unknown_schema"]["raw_bytes_durable"] = True
        changed, coherent = self._coherent(changed, "contracts/codex/rollout-and-inventory.yaml")
        self.assert_has(
            validate_codex.validate(changed, coherent, include_code=False),
            "corrupt/unknown-schema rollout records must never durably retain raw bytes",
        )

    def test_coherent_lock_mutation_cannot_weaken_unknown_agent_version_policy(self) -> None:
        base = validate_codex.registries()
        changed = copy.deepcopy(base)
        changed["contracts/codex/manifest.yaml"]["unknown_agent_version_policy"] = "unknown_versions_are_treated_as_fully_supported"
        changed, coherent = self._coherent(changed, "contracts/codex/manifest.yaml")
        self.assert_has(
            validate_codex.validate(changed, coherent, include_code=False),
            "unknown agent version outside every compatibility range must default to degraded",
        )

    def test_coherent_lock_mutation_cannot_relax_exit_gate_boolean_flags(self) -> None:
        base = validate_codex.registries()
        for flag in ("inventory_correct", "raw_prompt_absent_from_every_durable_path", "replay_idempotent"):
            with self.subTest(flag=flag):
                changed = copy.deepcopy(base)
                changed["contracts/codex/skill-evidence-and-reconciliation.yaml"]["exit_gate"][flag] = False
                mutated, coherent = self._coherent(changed, "contracts/codex/skill-evidence-and-reconciliation.yaml")
                self.assert_has(
                    validate_codex.validate(mutated, coherent, include_code=False),
                    f"exit gate {flag} must remain true",
                )

    def test_coherent_lock_mutation_cannot_change_semantic_opportunity_tier(self) -> None:
        base = validate_codex.registries()
        changed = copy.deepcopy(base)
        rows = changed["contracts/codex/skill-evidence-and-reconciliation.yaml"]["source_to_canonical_mapping"]
        for row in rows:
            if row["source_evidence"] == "semantic_opportunity_classifier":
                row["tier"] = "native"
        changed, coherent = self._coherent(changed, "contracts/codex/skill-evidence-and-reconciliation.yaml")
        self.assert_has(
            validate_codex.validate(changed, coherent, include_code=False),
            "semantic_opportunity_classifier mapping must remain tier inferred",
        )

    def test_top_level_schema_is_closed(self) -> None:
        base = validate_codex.registries()
        changed = copy.deepcopy(base)
        changed["contracts/codex/manifest.yaml"]["extra_unexpected_field"] = "surprise"
        self.assert_has(validate_codex.validate(changed, include_code=False), "manifest.yaml: top-level closed schema changed")

    def test_registry_set_must_be_exact(self) -> None:
        base = validate_codex.registries()
        missing = {k: v for k, v in base.items() if k != "contracts/codex/manifest.yaml"}
        self.assert_has(validate_codex.validate(missing, include_code=False), "codex registry set is not exact")

    def test_code_and_fixture_alignment_passes_standalone(self) -> None:
        self.assertEqual([], validate_codex.validate_code_and_fixtures())

    def test_fixture_files_are_all_synthetic(self) -> None:
        for name in (
            "hook-otel-golden-map.json", "inventory-layouts.json", "rollout-fixtures.json",
            "skill-collision-and-ambiguous-ownership.json", "prohibited-content-canaries.json",
        ):
            with self.subTest(name=name):
                fixture = validate_codex.load(validate_codex.FIXTURES_DIR / name)
                self.assertIs(fixture.get("synthetic"), True)

    def test_canary_scenario_fixture_is_synthetic_and_constrained(self) -> None:
        fixture = validate_codex.load(validate_codex.CANARY_SCENARIO_PATH)
        self.assertIs(fixture.get("synthetic"), True)
        constraints = fixture.get("execution_constraints", {})
        self.assertTrue(constraints.get("non_interactive_only"))
        self.assertTrue(constraints.get("never_uses_a_real_user_repository"))

    def test_fixtures_never_reference_a_real_home_directory_path(self) -> None:
        # Prohibited-content-canary fixtures (rollout-fixtures.json's
        # prohibited_content_canary, prohibited-content-canaries.json) exist
        # specifically to embed synthetic secret-shaped/PII-shaped strings
        # (marked SYNTHETIC_ONLY_never_persisted) as negative-test payloads
        # that Go tests assert never leak into a durable/accepted record --
        # so this check only guards against an actual real machine path
        # (which would indicate a fixture generator accidentally captured
        # live filesystem state) rather than banning synthetic secret-shaped
        # substrings outright.
        for path in sorted(validate_codex.FIXTURES_DIR.rglob("*.json")):
            with self.subTest(path=str(path.relative_to(validate_codex.ROOT))):
                text = path.read_text(encoding="utf-8")
                self.assertNotIn("/Users/", text)
                self.assertNotIn(str(validate_codex.ROOT), text)

    def test_prohibited_content_canary_payloads_are_explicitly_marked_synthetic(self) -> None:
        rollout_fixture = validate_codex.load(validate_codex.FIXTURES_DIR / "rollout-fixtures.json")
        canary = rollout_fixture.get("prohibited_content_canary", {})
        self.assertIn("SYNTHETIC_ONLY_never_persisted", canary.get("raw_content_line", ""))
        self.assertTrue(canary.get("forbidden_substrings"))

        canaries = validate_codex.load(validate_codex.FIXTURES_DIR / "prohibited-content-canaries.json")
        for case in canaries.get("canaries", []):
            with self.subTest(case=case.get("case")):
                self.assertTrue(case.get("forbidden_substrings_in_output") or case.get("forbidden_dropped_surfaces"))


if __name__ == "__main__":
    unittest.main()
