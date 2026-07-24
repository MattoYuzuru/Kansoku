from __future__ import annotations

import copy
import unittest

from scripts import validate_integrity


class Session08IntegrityContractTests(unittest.TestCase):
    def assert_has(self, errors: list[str], fragment: str) -> None:
        self.assertTrue(any(fragment in error for error in errors), errors)

    def coherent(self, changed: dict) -> dict:
        lock = copy.deepcopy(validate_integrity.load(validate_integrity.LOCK_PATH))
        for row in lock["locks"]:
            row["semantic_sha256"] = validate_integrity.semantic_sha256(changed[row["registry"]])
        return lock

    def test_contracts_code_fixtures_and_reports_validate(self) -> None:
        self.assertEqual([], validate_integrity.validate())

    def test_registry_set_is_exact(self) -> None:
        data = validate_integrity.registries()
        data.pop(next(iter(data)))
        self.assert_has(validate_integrity.validate(data, include_code=False), "registry set is not exact")

    def test_coherent_lock_cannot_change_stage_timeout(self) -> None:
        data = copy.deepcopy(validate_integrity.registries())
        data["contracts/integrity/audit-run-and-schedule.yaml"]["stage_registry"][1]["timeout_seconds"] = 999
        self.assert_has(validate_integrity.validate(data, self.coherent(data), include_code=False), "IDs/ordinals/timeouts")

    def test_coherent_lock_cannot_overload_source_into_capability(self) -> None:
        data = copy.deepcopy(validate_integrity.registries())
        data["contracts/integrity/audit-run-and-schedule.yaml"]["idempotency_rule"] = "audit_run_id check_id capability_id installation_id"
        self.assert_has(validate_integrity.validate(data, self.coherent(data), include_code=False), "lost source_id")

    def test_coherent_lock_cannot_hash_field_values(self) -> None:
        data = copy.deepcopy(validate_integrity.registries())
        data["contracts/integrity/drift-fingerprint-and-schema.yaml"]["structural_only_rule"] = "hash field values for accuracy"
        self.assert_has(validate_integrity.validate(data, self.coherent(data), include_code=False), "must never sample/hash values")

    def test_coherent_lock_cannot_move_shape_detection_back_to_stage3(self) -> None:
        data = copy.deepcopy(validate_integrity.registries())
        data["contracts/integrity/drift-fingerprint-and-schema.yaml"]["new_shape_counting_rule"] = "stage_3 owns schema parsing"
        self.assert_has(validate_integrity.validate(data, self.coherent(data), include_code=False), "stages 4 and 7")

    def test_coherent_lock_cannot_change_fault_class_or_slo(self) -> None:
        data = copy.deepcopy(validate_integrity.registries())
        row = data["contracts/integrity/fault-injection-and-live-canary.yaml"]["fault_injection_catalog"][0]
        row["expected_detection_slo_seconds"] = 999
        row["expected_incident_failure_class"] = "endpoint_unreachable"
        self.assert_has(validate_integrity.validate(data, self.coherent(data), include_code=False), "21 fault IDs/classes/SLOs")

    def test_runtime_fault_cannot_be_relabelled_component_evidence(self) -> None:
        data = copy.deepcopy(validate_integrity.registries())
        evidence = data["contracts/integrity/fault-injection-and-live-canary.yaml"]["evidence_classification_rule"]
        evidence["runtime_required"].remove("db_restart")
        evidence["component_classifier"].append("db_restart")
        self.assert_has(
            validate_integrity.validate(data, self.coherent(data), include_code=False),
            "fault evidence classification changed",
        )

    def test_coherent_lock_cannot_enable_shell_canary(self) -> None:
        data = copy.deepcopy(validate_integrity.registries())
        schema = data["contracts/integrity/fault-injection-and-live-canary.yaml"]["live_canary_recipe_schema"]
        schema["fields"]["command"] = "one shell string"
        self.assert_has(validate_integrity.validate(data, self.coherent(data), include_code=False), "never shell text")

    def test_coherent_lock_cannot_remove_consent_gate(self) -> None:
        data = copy.deepcopy(validate_integrity.registries())
        schema = data["contracts/integrity/fault-injection-and-live-canary.yaml"]["live_canary_recipe_schema"]
        schema["disabled_by_default_gate"]["rule"] = "enabled automatically"
        self.assert_has(validate_integrity.validate(data, self.coherent(data), include_code=False), "explicit_user_consent_recorded=true")

    def test_coherent_lock_cannot_drop_health_mapping(self) -> None:
        data = copy.deepcopy(validate_integrity.registries())
        health = data["contracts/integrity/incident-and-health.yaml"]
        health["failure_class_health_mapping"]["connectivity"].remove("endpoint_unreachable")
        self.assert_has(validate_integrity.validate(data, self.coherent(data), include_code=False), "cover all 23 classes")

    def test_lock_history_is_append_only(self) -> None:
        data = validate_integrity.registries()
        current = validate_integrity.load(validate_integrity.LOCK_PATH)
        reordered = copy.deepcopy(current)
        reordered["locks"][0], reordered["locks"][1] = reordered["locks"][1], reordered["locks"][0]
        self.assert_has(validate_integrity.validate(data, reordered, include_code=False, historical=current), "append-only trusted prefix")

    def test_policy_name_is_bound_to_registry(self) -> None:
        data = validate_integrity.registries()
        lock = copy.deepcopy(validate_integrity.load(validate_integrity.LOCK_PATH))
        lock["locks"][0]["policy_version"] = "integrity.incident-and-health/2"
        self.assert_has(
            validate_integrity.validate(data, lock, include_code=False),
            "policy name does not match registry identity",
        )


if __name__ == "__main__":
    unittest.main()
