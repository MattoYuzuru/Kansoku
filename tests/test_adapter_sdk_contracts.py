from __future__ import annotations

import copy
import unittest

from scripts import validate_adapter_sdk


class Session05AdapterSDKContractTests(unittest.TestCase):
    def assert_has(self, errors: list[str], fragment: str) -> None:
        self.assertTrue(any(fragment in error for error in errors), errors)

    def test_contracts_code_dependencies_and_fixture_validate(self) -> None:
        self.assertEqual([], validate_adapter_sdk.validate())

    def test_each_semantic_registry_is_policy_locked(self) -> None:
        base = validate_adapter_sdk.registries()
        for path in sorted(base):
            mutated = copy.deepcopy(base)
            mutated[path]["contract_version"] = "99.0.0"
            self.assert_has(validate_adapter_sdk.validate(mutated, include_code=False), "semantic digest changed")

    def test_coherent_lock_mutation_cannot_remove_no_agent_name_branch_invariant(self) -> None:
        base = validate_adapter_sdk.registries()
        locks = validate_adapter_sdk.load(validate_adapter_sdk.LOCK_PATH)
        changed = copy.deepcopy(base)
        changed["contracts/adapter-sdk/capabilities.yaml"]["no_brand_branch_invariant"] = "core_may_branch_on_agent_name_when_convenient"
        coherent = copy.deepcopy(locks)
        for item in coherent["locks"]:
            item["semantic_sha256"] = validate_adapter_sdk.semantic_sha256(changed[item["registry"]])
        self.assert_has(
            validate_adapter_sdk.validate(changed, coherent, include_code=False),
            "no-agent-name-branch invariant text weakened",
        )

    def test_coherent_lock_mutation_cannot_remove_brand_binding_rule(self) -> None:
        base = validate_adapter_sdk.registries()
        locks = validate_adapter_sdk.load(validate_adapter_sdk.LOCK_PATH)
        changed = copy.deepcopy(base)
        changed["contracts/adapter-sdk/capabilities.yaml"]["state_transitions"]["brand_binding"] = "ui_may_route_on_an_agent_brand_string"
        coherent = copy.deepcopy(locks)
        for item in coherent["locks"]:
            item["semantic_sha256"] = validate_adapter_sdk.semantic_sha256(changed[item["registry"]])
        self.assert_has(
            validate_adapter_sdk.validate(changed, coherent, include_code=False),
            "capability routing must bind to capability ids only",
        )

    def test_coherent_lock_mutation_cannot_remove_permission_scoping(self) -> None:
        base = validate_adapter_sdk.registries()
        locks = validate_adapter_sdk.load(validate_adapter_sdk.LOCK_PATH)
        mutations: list[tuple[str, dict[str, dict[str, object]]]] = []

        changed = copy.deepcopy(base)
        changed["contracts/adapter-sdk/discovery-and-plans.yaml"]["host_view_guarantee"] = "host_view_may_expose_a_database_credential_if_convenient"
        mutations.append(("HostView guarantee no longer excludes database credentials", changed))

        changed = copy.deepcopy(base)
        changed["contracts/adapter-sdk/manifest.yaml"]["network_grades"] = ["none", "loopback_only", "unrestricted"]
        mutations.append(("network grade set changed", changed))

        changed = copy.deepcopy(base)
        changed["contracts/adapter-sdk/discovery-and-plans.yaml"]["discovery_safety_rules"] = [
            rule for rule in changed["contracts/adapter-sdk/discovery-and-plans.yaml"]["discovery_safety_rules"]
            if "never_speculatively_scan_an_entire_home_directory" not in rule
        ]
        mutations.append(("never_speculatively_scan_an_entire_home_directory", changed))

        changed = copy.deepcopy(base)
        changed["contracts/adapter-sdk/manifest.yaml"]["parse_limits"]["code_execution"] = "permitted_for_trusted_manifests"
        mutations.append(("manifest parsing must explicitly forbid code execution", changed))

        for expected, candidate in mutations:
            coherent = copy.deepcopy(locks)
            for item in coherent["locks"]:
                item["semantic_sha256"] = validate_adapter_sdk.semantic_sha256(candidate[item["registry"]])
            with self.subTest(expected=expected):
                self.assert_has(validate_adapter_sdk.validate(candidate, coherent, include_code=False), expected)

    def test_change_plan_reuse_of_installer_machinery_cannot_be_silently_dropped(self) -> None:
        base = validate_adapter_sdk.registries()
        locks = validate_adapter_sdk.load(validate_adapter_sdk.LOCK_PATH)
        changed = copy.deepcopy(base)
        changed["contracts/adapter-sdk/discovery-and-plans.yaml"]["change_plan_reuse"] = "adaptersdk_invents_its_own_apply_rollback_mechanism"
        coherent = copy.deepcopy(locks)
        for item in coherent["locks"]:
            item["semantic_sha256"] = validate_adapter_sdk.semantic_sha256(changed[item["registry"]])
        self.assert_has(
            validate_adapter_sdk.validate(changed, coherent, include_code=False),
            "ChangePlan reuse of internal/installer machinery weakened",
        )

    def test_unknown_agent_version_cannot_be_silently_treated_as_healthy(self) -> None:
        base = validate_adapter_sdk.registries()
        changed = copy.deepcopy(base)
        changed["contracts/adapter-sdk/manifest.yaml"]["unknown_agent_version_policy"] = "unknown_versions_are_treated_as_fully_supported"
        self.assert_has(validate_adapter_sdk.validate(changed, include_code=False), "unknown agent version outside every compatibility range must default to degraded")

    def test_normal_operation_cannot_silently_apply_a_change_plan(self) -> None:
        base = validate_adapter_sdk.registries()
        changed = copy.deepcopy(base)
        changed["contracts/adapter-sdk/discovery-and-plans.yaml"]["normal_operation_rule"] = "collector_may_apply_a_change_plan_automatically_during_normal_operation"
        self.assert_has(validate_adapter_sdk.validate(changed, include_code=False), "normal collector operation must never apply a change plan")

    def test_policy_versions_are_contiguous_and_trusted_history_is_append_only(self) -> None:
        base = validate_adapter_sdk.registries()
        current = validate_adapter_sdk.load(validate_adapter_sdk.LOCK_PATH)
        changed = copy.deepcopy(base)
        changed["contracts/adapter-sdk/manifest.yaml"]["contract_version"] = "99.0.0"
        transitioned = copy.deepcopy(current)
        transitioned["locks"].append({
            "policy_version": "adapter-sdk.manifest/3",
            "registry": "contracts/adapter-sdk/manifest.yaml",
            "semantic_sha256": validate_adapter_sdk.semantic_sha256(changed["contracts/adapter-sdk/manifest.yaml"]),
        })
        self.assertEqual([], validate_adapter_sdk.validate(changed, transitioned, include_code=False, historical=current))

        reordered = copy.deepcopy(transitioned)
        reordered["locks"][0], reordered["locks"][1] = reordered["locks"][1], reordered["locks"][0]
        self.assert_has(validate_adapter_sdk.validate(changed, reordered, include_code=False, historical=current), "append-only trusted prefix")

        skipped = copy.deepcopy(current)
        skipped["locks"].append({
            "policy_version": "adapter-sdk.manifest/4",
            "registry": "contracts/adapter-sdk/manifest.yaml",
            "semantic_sha256": validate_adapter_sdk.semantic_sha256(changed["contracts/adapter-sdk/manifest.yaml"]),
        })
        self.assert_has(validate_adapter_sdk.validate(changed, skipped, include_code=False, historical=current), "start at 1 and remain contiguous")

    def test_fake_adapter_vocabulary_stays_out_of_the_real_agent_term_set(self) -> None:
        errors = validate_adapter_sdk.validate_code_and_fixture()
        self.assertEqual([], errors)

    def test_fixture_adapter_id_never_collides_with_a_real_agent_name(self) -> None:
        fixture = validate_adapter_sdk.load(validate_adapter_sdk.FIXTURE_PATH)
        self.assertNotIn(fixture.get("adapter_id"), validate_adapter_sdk.REAL_AGENT_TERMS)


if __name__ == "__main__":
    unittest.main()
