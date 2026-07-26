from __future__ import annotations

import copy
import unittest

from scripts import validate_observability


class Session03ObservabilityContractTests(unittest.TestCase):
    def assert_has(self, errors: list[str], fragment: str) -> None:
        self.assertTrue(any(fragment in error for error in errors), errors)

    def test_contracts_code_dependencies_and_fixture_validate(self) -> None:
        self.assertEqual([], validate_observability.validate())

    def test_each_semantic_registry_is_policy_locked(self) -> None:
        base = validate_observability.registries()
        for path in sorted(base):
            mutated = copy.deepcopy(base)
            mutated[path]["contract_version"] = "99.0.0"
            self.assert_has(validate_observability.validate(mutated, include_code=False), "semantic digest changed")

    def test_coherent_lock_mutation_cannot_weaken_closed_core_invariants(self) -> None:
        base = validate_observability.registries()
        locks = validate_observability.load(validate_observability.LOCK_PATH)
        mutations: list[tuple[str, dict[str, dict[str, object]]]] = []
        changed = copy.deepcopy(base); changed["contracts/observability/envelope.yaml"]["forbidden_fields"].remove("prompt"); mutations.append(("forbidden durable fields", changed))
        changed = copy.deepcopy(base); changed["contracts/observability/lifecycles.yaml"]["correlation_states"].remove("ambiguous"); mutations.append(("correlation/completeness", changed))
        changed = copy.deepcopy(base); changed["contracts/observability/ingress.yaml"]["protocols"] = [p for p in changed["contracts/observability/ingress.yaml"]["protocols"] if p["id"] != "otlp_grpc_traces"]; mutations.append(("protocol set", changed))
        changed = copy.deepcopy(base); changed["contracts/observability/ingress.yaml"]["unknown_schema"]["raw_bytes"] = True; mutations.append(("metadata-only", changed))
        changed = copy.deepcopy(base); changed["contracts/observability/ingress.yaml"]["durability"]["not_claimed"].remove("PostgreSQL"); mutations.append(("overclaimed", changed))
        changed = copy.deepcopy(base); changed["contracts/observability/reconciliation.yaml"]["expected_lanes"] = ["hook_http"]; mutations.append(("three-lane", changed))
        for expected, candidate in mutations:
            coherent = copy.deepcopy(locks)
            for item in coherent["locks"]:
                item["semantic_sha256"] = validate_observability.semantic_sha256(candidate[item["registry"]])
            with self.subTest(expected=expected):
                self.assert_has(validate_observability.validate(candidate, coherent, include_code=False), expected)

    def test_acknowledgement_cannot_move_before_durable_commit(self) -> None:
        base = validate_observability.registries()
        changed = copy.deepcopy(base)
        changed["contracts/observability/ingress.yaml"]["protocols"][0]["ack"] = "after_in_memory_decode"
        self.assert_has(validate_observability.validate(changed, include_code=False), "acknowledgement is not bound")

    def test_unknown_schema_cannot_be_silently_accepted_or_store_bytes(self) -> None:
        base = validate_observability.registries()
        for field, value in (("incident", "none"), ("raw_bytes", True)):
            changed = copy.deepcopy(base)
            changed["contracts/observability/ingress.yaml"]["unknown_schema"][field] = value
            self.assert_has(validate_observability.validate(changed, include_code=False), "metadata-only degraded")

    def test_inactivity_cannot_be_reclassified_as_gap(self) -> None:
        base = validate_observability.registries()
        changed = copy.deepcopy(base)
        changed["contracts/observability/reconciliation.yaml"]["silence"]["true_inactivity"] = "open incident"
        self.assert_has(validate_observability.validate(changed, include_code=False), "inactivity and source loss collapsed")

    def test_policy_versions_are_contiguous_and_trusted_history_is_append_only(self) -> None:
        base = validate_observability.registries()
        current = validate_observability.load(validate_observability.LOCK_PATH)
        envelope_versions = [
            int(entry["policy_version"].rsplit("/", 1)[1])
            for entry in current["locks"]
            if entry["registry"] == "contracts/observability/envelope.yaml"
        ]
        next_version = max(envelope_versions) + 1
        changed = copy.deepcopy(base)
        changed["contracts/observability/envelope.yaml"]["contract_version"] = "1.1.0"
        transitioned = copy.deepcopy(current)
        transitioned["locks"].append({
            "policy_version": f"observability.envelope/{next_version}",
            "registry": "contracts/observability/envelope.yaml",
            "semantic_sha256": validate_observability.semantic_sha256(changed["contracts/observability/envelope.yaml"]),
        })
        self.assertEqual([], validate_observability.validate(changed, transitioned, include_code=False, historical=current))
        reordered = copy.deepcopy(transitioned)
        reordered["locks"][0], reordered["locks"][1] = reordered["locks"][1], reordered["locks"][0]
        self.assert_has(validate_observability.validate(changed, reordered, include_code=False, historical=current), "append-only trusted prefix")
        skipped = copy.deepcopy(current)
        skipped["locks"].append({
            "policy_version": f"observability.envelope/{next_version + 1}",
            "registry": "contracts/observability/envelope.yaml",
            "semantic_sha256": validate_observability.semantic_sha256(changed["contracts/observability/envelope.yaml"]),
        })
        self.assert_has(validate_observability.validate(changed, skipped, include_code=False, historical=current), "start at 1 and remain contiguous")


if __name__ == "__main__":
    unittest.main()
