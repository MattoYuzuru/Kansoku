from __future__ import annotations

import copy
import unittest

from scripts import validate_claude


class Session07ClaudeContractTests(unittest.TestCase):
    def assert_has(self, errors: list[str], fragment: str) -> None:
        self.assertTrue(any(fragment in error for error in errors), errors)

    # -- full validate() --

    def test_contracts_code_dependencies_and_fixtures_validate(self) -> None:
        self.assertEqual([], validate_claude.validate())

    def test_code_and_fixture_alignment_passes_standalone(self) -> None:
        self.assertEqual([], validate_claude.validate_code_and_fixtures())

    def test_registry_set_must_be_exact(self) -> None:
        base = validate_claude.claude_registries()
        missing = {k: v for k, v in base.items() if k != "contracts/claude/manifest.yaml"}
        self.assert_has(validate_claude.validate_claude(missing, include_code=False), "claude registry set is not exact")

    def test_cross_agent_registry_set_must_be_exact(self) -> None:
        base = validate_claude.cross_agent_registries()
        missing = {k: v for k, v in base.items() if k != "contracts/cross-agent/second-fixture-agent.yaml"}
        self.assert_has(validate_claude.validate_cross_agent(missing), "cross-agent registry set is not exact")

    def test_top_level_schema_is_closed(self) -> None:
        base = validate_claude.claude_registries()
        changed = copy.deepcopy(base)
        changed["contracts/claude/manifest.yaml"]["extra_unexpected_field"] = "surprise"
        self.assert_has(validate_claude.validate_claude(changed, include_code=False), "manifest.yaml: top-level closed schema changed")

    # -- policy lock mechanics (claude) --

    def test_each_claude_semantic_registry_is_policy_locked(self) -> None:
        base = validate_claude.claude_registries()
        for path in sorted(base):
            mutated = copy.deepcopy(base)
            mutated[path]["contract_version"] = "99.0.0"
            self.assert_has(validate_claude.validate_claude(mutated, include_code=False), "semantic digest changed")

    def test_claude_policy_versions_are_contiguous_and_trusted_history_is_append_only(self) -> None:
        base = validate_claude.claude_registries()
        current = validate_claude.load(validate_claude.CLAUDE_LOCK_PATH)
        changed = copy.deepcopy(base)
        changed["contracts/claude/manifest.yaml"]["contract_version"] = "99.0.0"
        transitioned = copy.deepcopy(current)
        transitioned["locks"].append({
            "policy_version": "claude.manifest/3",
            "registry": "contracts/claude/manifest.yaml",
            "semantic_sha256": validate_claude.semantic_sha256(changed["contracts/claude/manifest.yaml"]),
        })
        self.assertEqual([], validate_claude.validate_claude(changed, transitioned, include_code=False, historical=current))

        reordered = copy.deepcopy(transitioned)
        reordered["locks"][0], reordered["locks"][1] = reordered["locks"][1], reordered["locks"][0]
        self.assert_has(validate_claude.validate_claude(changed, reordered, include_code=False, historical=current), "append-only trusted prefix")

        skipped = copy.deepcopy(current)
        skipped["locks"].append({
            "policy_version": "claude.manifest/4",
            "registry": "contracts/claude/manifest.yaml",
            "semantic_sha256": validate_claude.semantic_sha256(changed["contracts/claude/manifest.yaml"]),
        })
        self.assert_has(validate_claude.validate_claude(changed, skipped, include_code=False, historical=current), "start at 1 and remain contiguous")

    # -- policy lock mechanics (cross-agent) --

    def test_each_cross_agent_semantic_registry_is_policy_locked(self) -> None:
        base = validate_claude.cross_agent_registries()
        for path in sorted(base):
            mutated = copy.deepcopy(base)
            mutated[path]["contract_version"] = "99.0.0"
            self.assert_has(validate_claude.validate_cross_agent(mutated), "semantic digest changed")

    def test_cross_agent_policy_versions_are_contiguous_and_trusted_history_is_append_only(self) -> None:
        base = validate_claude.cross_agent_registries()
        current = validate_claude.load(validate_claude.CROSS_AGENT_LOCK_PATH)
        changed = copy.deepcopy(base)
        changed["contracts/cross-agent/second-fixture-agent.yaml"]["contract_version"] = "1.1.0"
        transitioned = copy.deepcopy(current)
        transitioned["locks"].append({
            "policy_version": "cross-agent.second-fixture-agent/2",
            "registry": "contracts/cross-agent/second-fixture-agent.yaml",
            "semantic_sha256": validate_claude.semantic_sha256(changed["contracts/cross-agent/second-fixture-agent.yaml"]),
        })
        self.assertEqual([], validate_claude.validate_cross_agent(changed, transitioned, historical=current))

        reordered = copy.deepcopy(transitioned)
        reordered["locks"][0], reordered["locks"][1] = reordered["locks"][1], reordered["locks"][0]
        self.assert_has(validate_claude.validate_cross_agent(changed, reordered, historical=current), "append-only trusted prefix")

        skipped = copy.deepcopy(current)
        skipped["locks"].append({
            "policy_version": "cross-agent.second-fixture-agent/3",
            "registry": "contracts/cross-agent/second-fixture-agent.yaml",
            "semantic_sha256": validate_claude.semantic_sha256(changed["contracts/cross-agent/second-fixture-agent.yaml"]),
        })
        self.assert_has(validate_claude.validate_cross_agent(changed, skipped, historical=current), "start at 1 and remain contiguous")

    # -- coherent lock mutation cannot remove a core invariant (claude) --

    def _coherent_claude(self, changed_registries: dict) -> dict:
        locks = validate_claude.load(validate_claude.CLAUDE_LOCK_PATH)
        coherent = copy.deepcopy(locks)
        for item in coherent["locks"]:
            item["semantic_sha256"] = validate_claude.semantic_sha256(changed_registries[item["registry"]])
        return coherent

    def test_coherent_lock_mutation_cannot_remove_never_persist_raw_prompt_invariant(self) -> None:
        base = validate_claude.claude_registries()
        changed = copy.deepcopy(base)
        changed["contracts/claude/hooks-and-otel.yaml"]["hook_source"]["helper_contract"]["prompt_feature_computation"] = (
            "prompt_features_are_computed_and_the_raw_prompt_may_also_be_forwarded_for_debugging"
        )
        coherent = self._coherent_claude(changed)
        self.assert_has(
            validate_claude.validate_claude(changed, coherent, include_code=False),
            "raw prompt text is never written to disk or sent",
        )

    def test_coherent_lock_mutation_cannot_remove_path_pseudonymization(self) -> None:
        base = validate_claude.claude_registries()
        changed = copy.deepcopy(base)
        changed["contracts/claude/hooks-and-otel.yaml"]["hook_source"]["helper_contract"]["path_pseudonymization"] = (
            "the_raw_transcript_path_and_cwd_may_be_forwarded_unmodified_for_debugging"
        )
        coherent = self._coherent_claude(changed)
        self.assert_has(
            validate_claude.validate_claude(changed, coherent, include_code=False),
            "hook helper must pseudonymize transcript_path/cwd",
        )

    def test_coherent_lock_mutation_cannot_remove_hook_trust_bypass_prohibition(self) -> None:
        base = validate_claude.claude_registries()
        changed = copy.deepcopy(base)
        changed["contracts/claude/hooks-and-otel.yaml"]["hook_source"]["trust_and_enabled_state"]["bypass_forbidden"] = (
            "kansoku_may_silently_repair_an_untrusted_or_disabled_hook_if_convenient"
        )
        coherent = self._coherent_claude(changed)
        self.assert_has(
            validate_claude.validate_claude(changed, coherent, include_code=False),
            "claude hook trust/enabled-state bypass_forbidden text weakened",
        )

    def test_coherent_lock_mutation_cannot_remove_unconditional_otel_strip_rule(self) -> None:
        base = validate_claude.claude_registries()
        changed = copy.deepcopy(base)
        changed["contracts/claude/hooks-and-otel.yaml"]["otel_source"]["unconditional_strip_rule"] = (
            "detailed_telemetry_content_fields_are_forwarded_when_the_upstream_setting_enables_them"
        )
        coherent = self._coherent_claude(changed)
        self.assert_has(
            validate_claude.validate_claude(changed, coherent, include_code=False),
            "claude.otel must strip detailed-telemetry content fields unconditionally",
        )

    def test_coherent_lock_mutation_cannot_redefine_otel_installer_target(self) -> None:
        base = validate_claude.claude_registries()
        changed = copy.deepcopy(base)
        changed["contracts/claude/manifest.yaml"]["installer_target_reuse"]["otel"] = (
            "a_new_claude_specific_otel_target_is_declared_here_instead_of_reusing_claude.user_otel"
        )
        coherent = self._coherent_claude(changed)
        self.assert_has(
            validate_claude.validate_claude(changed, coherent, include_code=False),
            "manifest must reuse contracts/privacy/installer.yaml's existing claude.user_otel target verbatim",
        )

    def test_coherent_lock_mutation_cannot_introduce_a_parallel_ingress_route(self) -> None:
        base = validate_claude.claude_registries()
        changed = copy.deepcopy(base)
        changed["contracts/claude/hooks-and-otel.yaml"]["hook_source"]["route"] = "/v1/claude/hooks/{event}"
        coherent = self._coherent_claude(changed)
        self.assert_has(
            validate_claude.validate_claude(changed, coherent, include_code=False),
            "claude.hook route must remain /v1/hooks/claude/{event}",
        )

    def test_coherent_lock_mutation_cannot_permit_project_local_default_scope(self) -> None:
        base = validate_claude.claude_registries()
        changed = copy.deepcopy(base)
        changed["contracts/claude/manifest.yaml"]["installer_target_reuse"]["project_local_scope"] = (
            "project_local_claude_config_is_the_default_scope_for_convenience"
        )
        coherent = self._coherent_claude(changed)
        self.assert_has(
            validate_claude.validate_claude(changed, coherent, include_code=False),
            "project-local Claude config scope must never be selected by default",
        )

    def test_coherent_lock_mutation_cannot_invent_a_new_capability_id(self) -> None:
        base = validate_claude.claude_registries()
        changed = copy.deepcopy(base)
        changed["contracts/claude/manifest.yaml"]["capability_ids"] = changed["contracts/claude/manifest.yaml"]["capability_ids"] + ["claude.special_capability"]
        coherent = self._coherent_claude(changed)
        self.assert_has(
            validate_claude.validate_claude(changed, coherent, include_code=False),
            "claude manifest capability_ids must reuse adapter-sdk's closed capability id list verbatim",
        )

    def test_coherent_lock_mutation_cannot_widen_network_grade(self) -> None:
        base = validate_claude.claude_registries()
        changed = copy.deepcopy(base)
        changed["contracts/claude/manifest.yaml"]["network_grade"] = "unrestricted"
        coherent = self._coherent_claude(changed)
        self.assert_has(
            validate_claude.validate_claude(changed, coherent, include_code=False),
            "claude adapter network grade must remain loopback_only",
        )

    def test_coherent_lock_mutation_cannot_permit_code_execution_in_parsing(self) -> None:
        base = validate_claude.claude_registries()
        changed = copy.deepcopy(base)
        changed["contracts/claude/manifest.yaml"]["reused_parse_limits"]["code_execution"] = "permitted_for_trusted_hook_helper_binaries"
        coherent = self._coherent_claude(changed)
        self.assert_has(
            validate_claude.validate_claude(changed, coherent, include_code=False),
            "manifest parsing must explicitly forbid code execution",
        )

    def test_coherent_lock_mutation_cannot_allow_speculative_home_scan(self) -> None:
        base = validate_claude.claude_registries()
        changed = copy.deepcopy(base)
        changed["contracts/claude/manifest.yaml"]["installation_discovery"]["never_speculative_home_scan"] = (
            "the_adapter_may_scan_the_entire_home_directory_when_no_documented_root_is_found"
        )
        coherent = self._coherent_claude(changed)
        self.assert_has(
            validate_claude.validate_claude(changed, coherent, include_code=False),
            "installation discovery must explicitly forbid scanning the entire home directory",
        )

    def test_coherent_lock_mutation_cannot_remove_native_exact_activation_prohibition(self) -> None:
        base = validate_claude.claude_registries()
        changed = copy.deepcopy(base)
        changed["contracts/claude/skill-evidence-and-reconciliation.yaml"]["skill_evidence_model"]["native_exact_activation_prohibition"] = (
            "reconstructed_or_inferred_tier_evidence_may_be_shown_as_a_native_exact_activation_when_convenient"
        )
        coherent = self._coherent_claude(changed)
        self.assert_has(
            validate_claude.validate_claude(changed, coherent, include_code=False),
            "native exact activation prohibition text weakened",
        )

    def test_coherent_lock_mutation_cannot_remove_ambiguous_ownership_rule(self) -> None:
        base = validate_claude.claude_registries()
        changed = copy.deepcopy(base)
        changed["contracts/claude/skill-evidence-and-reconciliation.yaml"]["skill_evidence_model"]["ambiguous_ownership_rule"] = (
            "an_ambiguous_helper_or_mcp_call_may_be_converted_to_component.invoked_when_only_one_candidate_seems_likely"
        )
        coherent = self._coherent_claude(changed)
        self.assert_has(
            validate_claude.validate_claude(changed, coherent, include_code=False),
            "ambiguous ownership rule weakened",
        )

    def test_coherent_lock_mutation_cannot_remove_unsupported_rendering_rule(self) -> None:
        base = validate_claude.claude_registries()
        changed = copy.deepcopy(base)
        changed["contracts/claude/skill-evidence-and-reconciliation.yaml"]["skill_evidence_model"]["unsupported_rendering_rule"] = (
            "an_unsupported_field_may_render_as_a_zero_value_for_simplicity"
        )
        coherent = self._coherent_claude(changed)
        self.assert_has(
            validate_claude.validate_claude(changed, coherent, include_code=False),
            "unsupported rendering rule weakened",
        )

    def test_coherent_lock_mutation_cannot_remove_missing_source_degradation_rule(self) -> None:
        base = validate_claude.claude_registries()
        changed = copy.deepcopy(base)
        changed["contracts/claude/skill-evidence-and-reconciliation.yaml"]["reconciliation"]["missing_source_rule"] = (
            "missing_one_expected_source_reports_zero_usage_for_the_whole_session_for_simplicity"
        )
        coherent = self._coherent_claude(changed)
        self.assert_has(
            validate_claude.validate_claude(changed, coherent, include_code=False),
            "missing-source reconciliation rule must degrade only that source",
        )

    def test_coherent_lock_mutation_cannot_drop_independent_source_degradation(self) -> None:
        base = validate_claude.claude_registries()
        changed = copy.deepcopy(base)
        changed["contracts/claude/hooks-and-otel.yaml"]["independent_capability_degradation"] = (
            "disabling_claude.hook_also_zeroes_out_claude.otel_evidence_for_simplicity"
        )
        coherent = self._coherent_claude(changed)
        self.assert_has(
            validate_claude.validate_claude(changed, coherent, include_code=False),
            "hook/OTel/transcript independent capability degradation guarantee weakened",
        )

    def test_coherent_lock_mutation_cannot_weaken_unknown_agent_version_policy(self) -> None:
        base = validate_claude.claude_registries()
        changed = copy.deepcopy(base)
        changed["contracts/claude/manifest.yaml"]["unknown_agent_version_policy"] = "unknown_versions_are_treated_as_fully_supported"
        coherent = self._coherent_claude(changed)
        self.assert_has(
            validate_claude.validate_claude(changed, coherent, include_code=False),
            "unknown agent version outside every compatibility range must default to degraded",
        )

    def test_coherent_lock_mutation_cannot_allow_durable_raw_bytes_on_quarantine(self) -> None:
        base = validate_claude.claude_registries()
        changed = copy.deepcopy(base)
        changed["contracts/claude/transcript-and-inventory.yaml"]["transcript_source"]["corrupt_or_unknown_schema"]["raw_bytes_durable"] = True
        coherent = self._coherent_claude(changed)
        self.assert_has(
            validate_claude.validate_claude(changed, coherent, include_code=False),
            "corrupt/unknown-schema transcript records must never durably retain raw bytes",
        )

    def test_coherent_lock_mutation_cannot_make_transcript_replay_non_idempotent(self) -> None:
        base = validate_claude.claude_registries()
        changed = copy.deepcopy(base)
        changed["contracts/claude/transcript-and-inventory.yaml"]["transcript_source"]["replay_and_crash"]["idempotency"] = (
            "replaying_the_same_byte_range_twice_may_yield_a_duplicate_fact"
        )
        coherent = self._coherent_claude(changed)
        self.assert_has(
            validate_claude.validate_claude(changed, coherent, include_code=False),
            "transcript replay idempotency guarantee weakened",
        )

    def test_coherent_lock_mutation_cannot_widen_cache_separation_rule(self) -> None:
        base = validate_claude.claude_registries()
        changed = copy.deepcopy(base)
        changed["contracts/claude/transcript-and-inventory.yaml"]["inventory_source"]["cache_rule"] = (
            "cache_packages_may_be_considered_enabled_if_recently_installed"
        )
        coherent = self._coherent_claude(changed)
        self.assert_has(
            validate_claude.validate_claude(changed, coherent, include_code=False),
            "claude.inventory cache separation rule weakened",
        )

    def test_coherent_lock_mutation_cannot_allow_recursive_repository_scan(self) -> None:
        base = validate_claude.claude_registries()
        changed = copy.deepcopy(base)
        changed["contracts/claude/transcript-and-inventory.yaml"]["inventory_source"]["repository_scan_bound"] = (
            "repository_roots_may_be_discovered_via_a_speculative_recursive_filesystem_walk"
        )
        coherent = self._coherent_claude(changed)
        self.assert_has(
            validate_claude.validate_claude(changed, coherent, include_code=False),
            "claude.inventory repository scan must never be a speculative recursive filesystem walk",
        )

    def test_coherent_lock_mutation_cannot_change_semantic_opportunity_tier(self) -> None:
        base = validate_claude.claude_registries()
        changed = copy.deepcopy(base)
        rows = changed["contracts/claude/skill-evidence-and-reconciliation.yaml"]["source_to_canonical_mapping"]
        for row in rows:
            if row["source_evidence"] == "semantic_opportunity_classifier":
                row["tier"] = "native"
        coherent = self._coherent_claude(changed)
        self.assert_has(
            validate_claude.validate_claude(changed, coherent, include_code=False),
            "semantic_opportunity_classifier mapping must remain tier inferred",
        )

    def test_coherent_lock_mutation_cannot_relax_exit_gate_boolean_flags(self) -> None:
        base = validate_claude.claude_registries()
        for flag in ("inventory_correct", "raw_prompt_absent_from_every_durable_path", "replay_idempotent"):
            with self.subTest(flag=flag):
                changed = copy.deepcopy(base)
                changed["contracts/claude/skill-evidence-and-reconciliation.yaml"]["exit_gate"][flag] = False
                coherent = self._coherent_claude(changed)
                self.assert_has(
                    validate_claude.validate_claude(changed, coherent, include_code=False),
                    f"exit gate {flag} must remain true",
                )

    def test_coherent_lock_mutation_cannot_assert_support_label_ahead_of_evidence(self) -> None:
        base = validate_claude.claude_registries()
        changed = copy.deepcopy(base)
        changed["contracts/claude/skill-evidence-and-reconciliation.yaml"]["exit_gate"]["support_label_governance"] = (
            "the_support_label_may_be_asserted_as_production_ahead_of_any_evidence_being_produced"
        )
        coherent = self._coherent_claude(changed)
        self.assert_has(
            validate_claude.validate_claude(changed, coherent, include_code=False),
            "support_label_governance must state Claude's exact support label is never asserted ahead of actually produced evidence",
        )

    # -- coherent lock mutation cannot remove a core invariant (cross-agent) --

    def _coherent_cross(self, changed_registries: dict) -> dict:
        locks = validate_claude.load(validate_claude.CROSS_AGENT_LOCK_PATH)
        coherent = copy.deepcopy(locks)
        for item in coherent["locks"]:
            item["semantic_sha256"] = validate_claude.semantic_sha256(changed_registries[item["registry"]])
        return coherent

    def test_coherent_lock_mutation_cannot_remove_zero_core_branch_requirement(self) -> None:
        base = validate_claude.cross_agent_registries()
        changed = copy.deepcopy(base)
        changed["contracts/cross-agent/second-fixture-agent.yaml"]["required_conformance_checks"] = [
            check for check in changed["contracts/cross-agent/second-fixture-agent.yaml"]["required_conformance_checks"]
            if "zero_new_if_agentid_branch_inside_internal/adaptersdk" not in check.lower()
        ]
        coherent = self._coherent_cross(changed)
        self.assert_has(
            validate_claude.validate_cross_agent(changed, coherent),
            "second fixture-agent required_conformance_checks must require zero new agent-name branch",
        )

    def test_coherent_lock_mutation_cannot_populate_missing_token_capability_with_zero(self) -> None:
        base = validate_claude.cross_agent_registries()
        changed = copy.deepcopy(base)
        changed["contracts/cross-agent/second-fixture-agent.yaml"]["shape_deliberately_unlike_loomwright_and_real_adapters"]["missing_token_capability"] = (
            "the_missing_token_capability_is_populated_with_a_placeholder_zero_for_dashboard_simplicity"
        )
        coherent = self._coherent_cross(changed)
        self.assert_has(
            validate_claude.validate_cross_agent(changed, coherent),
            "second fixture-agent missing token capability must never be populated with a placeholder zero",
        )

    def test_coherent_lock_mutation_cannot_silently_drop_unknown_schema_event(self) -> None:
        base = validate_claude.cross_agent_registries()
        changed = copy.deepcopy(base)
        changed["contracts/cross-agent/second-fixture-agent.yaml"]["shape_deliberately_unlike_loomwright_and_real_adapters"]["one_deliberately_unknown_schema"] = (
            "the_unknown_schema_event_is_silently_dropped_rather_than_quarantined"
        )
        coherent = self._coherent_cross(changed)
        self.assert_has(
            validate_claude.validate_cross_agent(changed, coherent),
            "second fixture-agent's one deliberately unknown schema event must be quarantined",
        )

    def test_coherent_lock_mutation_cannot_widen_participating_adapters(self) -> None:
        base = validate_claude.cross_agent_registries()
        changed = copy.deepcopy(base)
        changed["contracts/cross-agent/invariant-scenario.yaml"]["participating_adapters"] = ["codex", "claude", "gemini"]
        coherent = self._coherent_cross(changed)
        self.assert_has(
            validate_claude.validate_cross_agent(changed, coherent),
            "cross-agent invariant scenario participating_adapters must remain exactly [codex, claude]",
        )

    def test_coherent_lock_mutation_cannot_permit_agent_id_string_equality_assertion(self) -> None:
        base = validate_claude.cross_agent_registries()
        changed = copy.deepcopy(base)
        changed["contracts/cross-agent/invariant-scenario.yaml"]["assertion_rule"] = (
            "the_test_may_assert_directly_on_a_string_equality_check_against_codex_or_claude_as_an_agent_id_for_convenience"
        )
        coherent = self._coherent_cross(changed)
        self.assert_has(
            validate_claude.validate_cross_agent(changed, coherent),
            "cross-agent invariant scenario assertion_rule must forbid asserting on an agent-id string equality check",
        )

    def test_coherent_lock_mutation_cannot_force_equal_evidence_tiers_or_uniform_zero(self) -> None:
        base = validate_claude.cross_agent_registries()
        changed = copy.deepcopy(base)
        changed["contracts/cross-agent/invariant-scenario.yaml"]["unsupported_rendering_rule"] = (
            "the_test_may_normalize_both_agents_to_a_uniform_zero_or_force_equal_evidence_tiers_for_simplicity"
        )
        coherent = self._coherent_cross(changed)
        self.assert_has(
            validate_claude.validate_cross_agent(changed, coherent),
            "cross-agent invariant scenario unsupported_rendering_rule must forbid forcing equal evidence tiers or a uniform zero across both agents",
        )

    # -- fixtures --

    def test_fixture_files_are_all_present_and_synthetic(self) -> None:
        wayfinder_fixture = validate_claude.load(validate_claude.FIXTURES_DIR / "wayfinder-eventfile.json")
        self.assertEqual(wayfinder_fixture.get("adapter_id"), "wayfinder")
        scenario_fixture = validate_claude.load(validate_claude.FIXTURES_DIR / "cross-agent-invariant-scenario.json")
        self.assertIn("codex", scenario_fixture)
        self.assertIn("claude", scenario_fixture)

    def test_fixtures_never_reference_a_real_home_directory_path(self) -> None:
        for path in sorted(validate_claude.FIXTURES_DIR.rglob("*.json")):
            with self.subTest(path=str(path.relative_to(validate_claude.ROOT))):
                text = path.read_text(encoding="utf-8")
                self.assertNotIn("/Users/", text)
                self.assertNotIn(str(validate_claude.ROOT), text)


if __name__ == "__main__":
    unittest.main()
