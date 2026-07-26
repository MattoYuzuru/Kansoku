from __future__ import annotations

import copy
import json
import unittest
from pathlib import Path

from scripts import validate_privacy


ROOT = Path(__file__).resolve().parents[1]


class Session02PrivacyContractTests(unittest.TestCase):
    def assert_has(self, errors: list[str], fragment: str) -> None:
        self.assertTrue(any(fragment in error for error in errors), errors)

    def test_all_machine_readable_privacy_contracts_validate(self) -> None:
        self.assertEqual([], validate_privacy.validate_all(include_documentation=False))

    def test_prohibited_class_cannot_be_made_durable_loggable_or_exportable(self) -> None:
        data = validate_privacy.registry("data-classes.yaml")
        for field in ("durable", "logging", "export", "backup"):
            mutated = copy.deepcopy(data)
            prohibited = next(item for item in mutated["classes"] if item["id"] == "prohibited_content")
            prohibited[field] = True
            self.assert_has(validate_privacy.validate_data_classes(mutated), f"{field} must be false")

    def test_canonical_value_states_cannot_collapse_redacted_unknown_or_zero(self) -> None:
        data = validate_privacy.registry("data-classes.yaml")
        mutated = copy.deepcopy(data)
        mutated["state_invariants"]["canonical_value_states"].remove("redacted")
        self.assert_has(validate_privacy.validate_data_classes(mutated), "canonical states")

    def test_every_output_sink_is_mandatory_and_typed(self) -> None:
        data = validate_privacy.registry("sinks.yaml")
        for sink_id in sorted(validate_privacy.EXPECTED_SINKS):
            mutated = copy.deepcopy(data)
            mutated["required_sinks"] = [item for item in mutated["required_sinks"] if item["id"] != sink_id]
            self.assert_has(validate_privacy.validate_sinks(mutated), "expected exact closed set")
        mutated = copy.deepcopy(data)
        mutated["required_sinks"][0]["notes"] = "untyped bypass"
        self.assert_has(validate_privacy.validate_sinks(mutated), "typed id/slo_scope/durable")

    def test_installer_cannot_skip_preview_consent_race_or_removal_contract(self) -> None:
        data = validate_privacy.registry("installer.yaml")
        for stage in ("exact_preview", "per_target_explicit_consent", "prewrite_revision_check", "revision_checked_rollback_or_remove"):
            mutated = copy.deepcopy(data)
            mutated["protocol"].remove(stage)
            self.assert_has(validate_privacy.validate_installer(mutated), f"missing {stage}")
        mutated = copy.deepcopy(data)
        mutated["targets"] = mutated["targets"][:-1]
        self.assert_has(validate_privacy.validate_installer(mutated), "expected exact target set")
        mutated = copy.deepcopy(data)
        mutated["implementation_scope_session_02"] = "write live configs"
        self.assert_has(validate_privacy.validate_installer(mutated), "must not claim or perform real")

    def test_host_access_is_closed_world_and_runtime_read_only(self) -> None:
        data = validate_privacy.registry("host-access.yaml")
        mutated = copy.deepcopy(data)
        mutated["accesses"].append({
            "id": "home_mount", "actor": "adapter", "mode": "write", "scope": "home root",
            "default_enabled": True, "justification": "shortcut", "disable_remove": "unknown",
        })
        self.assert_has(validate_privacy.validate_host_access(mutated), "expected exact closed world")
        mutated = copy.deepcopy(data)
        mutated["runtime_agent_access"] = "runtime may edit agent config"
        self.assert_has(validate_privacy.validate_host_access(mutated), "read-only toward agents")
        mutated = copy.deepcopy(data)
        identity = next(item for item in mutated["accesses"] if item["id"] == "identity_key_file")
        identity["scope"] = "arbitrary key file"
        self.assert_has(validate_privacy.validate_host_access(mutated), "0600/nlink1")

    def test_compose_mutations_fail_loopback_mount_capability_image_and_egress_policy(self) -> None:
        deployment = validate_privacy.registry("deployment.yaml")
        compose = validate_privacy.load(ROOT / "deploy" / "compose.security-baseline.yaml")
        mutations = []
        changed = copy.deepcopy(compose); changed["services"]["app"]["ports"] = ["127.0.0.1:3000:3000"]; mutations.append((changed, "publish no unusable ports"))
        changed = copy.deepcopy(compose); changed["services"]["app"]["volumes"].append("/Users/private:/host:rw"); mutations.append((changed, "host bind mount"))
        changed = copy.deepcopy(compose); changed["services"]["app"]["cap_drop"] = []; mutations.append((changed, "cap_drop ALL"))
        changed = copy.deepcopy(compose); changed["services"]["database"]["image"] = "postgres:latest"; mutations.append((changed, "immutable digest"))
        changed = copy.deepcopy(compose); changed["networks"]["kansoku-internal"]["internal"] = False; mutations.append((changed, "internal default-deny"))
        changed = copy.deepcopy(compose); changed["services"]["database"]["ports"] = ["5432:5432"]; mutations.append((changed, "must not be published"))
        for changed, expected in mutations:
            with self.subTest(expected=expected):
                self.assert_has(validate_privacy.validate_deployment(deployment, changed), expected)

    def test_retention_covers_exports_backups_and_identity_key_exclusion(self) -> None:
        data = validate_privacy.registry("retention.yaml")
        mutated = copy.deepcopy(data)
        mutated["surfaces"].remove("weekly backups")
        self.assert_has(validate_privacy.validate_retention(mutated), "every live/derived/export/backup")
        mutated = copy.deepcopy(data)
        mutated["identity_key"]["included_in_backup"] = True
        self.assert_has(validate_privacy.validate_retention(mutated), "identity key must be excluded")

    def test_every_threat_has_known_controls_and_abuse_case_tests(self) -> None:
        data = validate_privacy.registry("threat-model.yaml")
        mutated = copy.deepcopy(data)
        mutated["threats"][0]["controls"] = ["UNKNOWN-CONTROL"]
        self.assert_has(validate_privacy.validate_threat_model(mutated), "unknown controls")
        mutated = copy.deepcopy(data)
        mutated["threats"][0]["tests"] = []
        self.assert_has(validate_privacy.validate_threat_model(mutated), "abuse-case tests required")

    def test_raw_canary_fixture_is_synthetic_and_has_every_prohibited_family(self) -> None:
        fixture = json.loads((ROOT / "tests" / "fixtures" / "session-02" / "raw-canary-input.json").read_text(encoding="utf-8"))
        expected = {"prompt", "response", "source_code", "tool_input", "tool_output", "command", "path", "environment", "credential", "high_entropy", "exception", "attachment"}
        self.assertEqual(expected, set(fixture["canaries"]))
        serialized = json.dumps(fixture, sort_keys=True)
        self.assertNotIn("/Users/", serialized)
        self.assertNotIn("@example.com", serialized)

    def test_go_boundary_has_typed_exact_safe_structs_and_virtual_installer_only(self) -> None:
        self.assertEqual([], validate_privacy.validate_go_boundary())

    def test_every_registry_is_recursively_closed_against_extra_missing_and_wrong_values(self) -> None:
        validators = {
            "data-classes.yaml": validate_privacy.validate_data_classes,
            "threat-model.yaml": validate_privacy.validate_threat_model,
            "ingress.yaml": validate_privacy.validate_ingress,
            "sinks.yaml": validate_privacy.validate_sinks,
            "installer.yaml": validate_privacy.validate_installer,
            "host-access.yaml": validate_privacy.validate_host_access,
            "deployment.yaml": validate_privacy.validate_deployment,
            "retention.yaml": validate_privacy.validate_retention,
        }
        for name, validator in validators.items():
            source = validate_privacy.registry(name)
            mutations = []
            changed = copy.deepcopy(source); changed["unexpected_free_text"] = "raw prompt logging is fine"; mutations.append(changed)
            changed = copy.deepcopy(source); changed.pop("contract_version"); mutations.append(changed)
            changed = copy.deepcopy(source); changed["contract_version"] = True; mutations.append(changed)
            for changed in mutations:
                with self.subTest(registry=name):
                    self.assert_has(validator(changed), "authoritative recursive registry semantics changed")

    def test_nested_ingress_installer_and_deployment_security_values_cannot_be_weakened(self) -> None:
        ingress = validate_privacy.registry("ingress.yaml")
        mutations = []
        changed = copy.deepcopy(ingress); changed["durable_record_fields"].append("raw_prompt"); mutations.append(changed)
        changed = copy.deepcopy(ingress); changed["nested_types"]["PromptFeatures"]["fields"]["raw_text"] = "string"; mutations.append(changed)
        changed = copy.deepcopy(ingress); del changed["nested_types"]["Lineage"]["fields"]["contract_sha256"]; mutations.append(changed)
        changed = copy.deepcopy(ingress); changed["decoder_policy"]["duplicate_names"] = "last_wins"; mutations.append(changed)
        for changed in mutations:
            self.assert_has(validate_privacy.validate_ingress(changed), "authoritative recursive")
        installer = validate_privacy.registry("installer.yaml")
        for field, value in (("session_02_real_write", True), ("runtime_canary", "optional")):
            changed = copy.deepcopy(installer); changed["effective_settings_gate"][field] = value
            self.assert_has(validate_privacy.validate_installer(changed), "authoritative recursive")
        changed = copy.deepcopy(installer); changed["targets"][2]["required_settings"]["telemetry.logPrompts"] = True
        self.assert_has(validate_privacy.validate_installer(changed), "authoritative recursive")
        deployment = validate_privacy.registry("deployment.yaml")
        changed = copy.deepcopy(deployment); changed["http"]["allowed_hosts"].append("evil.invalid")
        self.assert_has(validate_privacy.validate_deployment(changed), "authoritative recursive")
        changed = copy.deepcopy(deployment); changed["controls"][0]["requirements"] = []
        self.assert_has(validate_privacy.validate_deployment(changed), "authoritative recursive")

    def test_privacy_sink_ids_map_one_to_one_to_raw_content_slo_scopes(self) -> None:
        sinks = validate_privacy.registry("sinks.yaml")
        changed = copy.deepcopy(sinks); changed["required_sinks"][0]["slo_scope"] = changed["required_sinks"][1]["slo_scope"]
        self.assert_has(validate_privacy.validate_sinks(changed), "one-to-one")

    def test_runtime_registry_hash_binding_is_current(self) -> None:
        self.assertEqual([], validate_privacy.validate_registry_runtime_binding())

    def test_review_controlled_policy_rejects_nine_coherent_registry_runtime_checksum_mutations(self) -> None:
        base = validate_privacy.registry_set()
        mutations: list[tuple[str, str, dict[str, dict[str, object]]]] = []

        changed = copy.deepcopy(base)
        changed["contracts/privacy/ingress.yaml"]["source_schemas"][0]["input_fields"].append("raw_payload")
        changed["contracts/privacy/ingress.yaml"]["source_schemas"][0]["models"].append("catalog/model-unreviewed")
        mutations.append(("source and catalog allowlists", "exact source input and catalog", changed))

        changed = copy.deepcopy(base)
        changed["contracts/privacy/ingress.yaml"]["nested_types"]["Lineage"]["fields"]["raw_text"] = "string"
        mutations.append(("nested Go schema", "full nested Go schemas", changed))

        changed = copy.deepcopy(base)
        changed["contracts/privacy/ingress.yaml"]["nested_types"]["PromptFeatures"]["fields"]["free_text"] = "string"
        mutations.append(("raw/content feature", "full nested Go schemas", changed))

        changed = copy.deepcopy(base)
        changed["contracts/privacy/ingress.yaml"]["privacy_safe_log_fields"].append("content")
        changed["contracts/privacy/ingress.yaml"]["nested_types"]["SafeLogEvent"]["fields"]["content"] = "string"
        mutations.append(("safe log schema", "privacy-safe log field schema", changed))

        changed = copy.deepcopy(base)
        gemini = next(item for item in changed["contracts/privacy/installer.yaml"]["targets"] if item["id"] == "gemini.user_otel")
        gemini["required_settings"]["telemetry.logPrompts"] = True
        gemini["required_settings"]["telemetry.target"] = "gcp"
        gemini["forbidden_keys"].remove("telemetry.outfile")
        mutations.append(("Gemini installer", "gemini.user_otel", changed))

        changed = copy.deepcopy(base)
        identity = next(item for item in changed["contracts/privacy/host-access.yaml"]["accesses"] if item["id"] == "identity_key_file")
        identity.update({"mode": "read_write", "scope": "home root", "default_enabled": False})
        mutations.append(("host access", "identity_key_file", changed))

        changed = copy.deepcopy(base)
        changed["contracts/privacy/deployment.yaml"]["http"]["allowed_hosts"].append("evil.invalid")
        changed["contracts/privacy/deployment.yaml"]["http"]["allowed_origins"].append("https://evil.invalid")
        mutations.append(("loopback hosts/origins", "loopback-only hosts", changed))

        changed = copy.deepcopy(base)
        changed["contracts/privacy/deployment.yaml"]["controls"][0]["requirements"] = []
        mutations.append(("empty controls", "nonempty deployment controls", changed))

        changed = copy.deepcopy(base)
        changed["contracts/privacy/deployment.yaml"]["http"]["route_modes"]["hook_otlp"]["methods"].append("GET")
        mutations.append(("GET hook_otlp", "hook_otlp must prohibit GET", changed))

        original_aggregate = validate_privacy.privacy_registry_sha256(base)
        for name, expected, candidate in mutations:
            with self.subTest(name=name):
                coherent_runtime_hash = validate_privacy.privacy_registry_sha256(candidate)
                self.assertNotEqual(original_aggregate, coherent_runtime_hash)
                self.assertRegex(coherent_runtime_hash, r"^[0-9a-f]{64}$")
                errors = validate_privacy.validate_security_policy_candidate(candidate, coherent_runtime_hash)
                self.assert_has(errors, expected)
                self.assert_has(errors, "semantics changed without a new reviewed policy-version lock")

    def test_privacy_policy_lock_bootstrap_history_and_version_transition_are_deterministic(self) -> None:
        current_lock = validate_privacy.load(validate_privacy.POLICY_LOCK_PATH)
        base = validate_privacy.registry_set()
        self.assertEqual([], validate_privacy.validate_privacy_policy_locks(current_lock, None, base))
        self.assertEqual(
            validate_privacy.validate_privacy_policy_locks(current_lock, None, base),
            validate_privacy.validate_privacy_policy_locks(copy.deepcopy(current_lock), None, copy.deepcopy(base)),
        )
        ingress_versions = [
            int(entry["policy_version"].rsplit("/", 1)[1])
            for entry in current_lock["locks"]
            if entry["registry"] == "contracts/privacy/ingress.yaml"
        ]
        next_version = max(ingress_versions) + 1

        candidate = copy.deepcopy(base)
        candidate["contracts/privacy/ingress.yaml"]["contract_version"] = "1.1.0"
        transitioned = copy.deepcopy(current_lock)
        transitioned["locks"].append({
            "policy_version": f"privacy.ingress/{next_version}",
            "registry": "contracts/privacy/ingress.yaml",
            "semantic_sha256": validate_privacy.canonical_semantic_sha256(candidate["contracts/privacy/ingress.yaml"]),
        })
        self.assertEqual([], validate_privacy.validate_privacy_policy_locks(transitioned, current_lock, candidate))

        replaced = copy.deepcopy(transitioned)
        replaced["locks"] = [entry for entry in replaced["locks"] if entry["policy_version"] != "privacy.ingress/1"]
        self.assert_has(
            validate_privacy.validate_privacy_policy_locks(replaced, current_lock, candidate),
            "historical entry privacy.ingress/1 was removed or changed",
        )
        rewritten = copy.deepcopy(transitioned)
        next(entry for entry in rewritten["locks"] if entry["policy_version"] == "privacy.ingress/1")["semantic_sha256"] = "0" * 64
        self.assert_has(
            validate_privacy.validate_privacy_policy_locks(rewritten, current_lock, candidate),
            "historical entry privacy.ingress/1 was removed or changed",
        )
        reordered = copy.deepcopy(transitioned)
        reordered["locks"][0], reordered["locks"][1] = reordered["locks"][1], reordered["locks"][0]
        self.assert_has(
            validate_privacy.validate_privacy_policy_locks(reordered, current_lock, candidate),
            "exact append-only prefix",
        )
        skipped = copy.deepcopy(current_lock)
        skipped["locks"].append({
            "policy_version": f"privacy.ingress/{next_version + 1}",
            "registry": "contracts/privacy/ingress.yaml",
            "semantic_sha256": validate_privacy.canonical_semantic_sha256(candidate["contracts/privacy/ingress.yaml"]),
        })
        self.assert_has(
            validate_privacy.validate_privacy_policy_locks(skipped, current_lock, candidate),
            "versions must start at 1 and remain contiguous",
        )

    def test_privacy_policy_history_supports_archive_bootstrap_and_optional_or_required_trusted_head(self) -> None:
        archive_errors = validate_privacy.validate_all(
            include_documentation=False, policy_history_ref=None,
        )
        self.assertEqual([], archive_errors)
        head = validate_privacy.git_privacy_policy_history("HEAD")
        if head is not None:
            self.assertEqual([], validate_privacy.policy_lock_entries(head, "HEAD policy locks")[0])
        self.assertIsNone(validate_privacy.git_privacy_policy_history("refs/kansoku-missing-policy-lock", required=False))
        with self.assertRaisesRegex(ValueError, "required trusted history unavailable"):
            validate_privacy.git_privacy_policy_history("refs/kansoku-missing-policy-lock", required=True)


if __name__ == "__main__":
    unittest.main()
