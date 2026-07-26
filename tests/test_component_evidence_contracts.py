from __future__ import annotations

import copy
import unittest

from scripts import validate_component_evidence


class Session14ComponentEvidenceContractTests(unittest.TestCase):
    def assert_has(self, errors: list[str], fragment: str) -> None:
        self.assertTrue(any(fragment in error for error in errors), errors)

    def test_contract_lock_and_code_validate(self) -> None:
        self.assertEqual([], validate_component_evidence.validate())

    def test_terminal_contract_and_no_winner_rules_are_closed(self) -> None:
        contract = validate_component_evidence.load(validate_component_evidence.CONTRACT_PATH)
        lock = validate_component_evidence.load(validate_component_evidence.LOCK_PATH)
        changed = copy.deepcopy(contract)
        changed["identity"]["multiple_matches"] = "select the first candidate"
        changed["assertion"]["outcome_rule"] = "session success is enough"
        coherent = copy.deepcopy(lock)
        coherent["locks"][-1]["semantic_sha256"] = validate_component_evidence.semantic_sha256(changed)
        errors = validate_component_evidence.validate(changed, coherent, include_code=False)
        self.assert_has(errors, "never select a winner")
        self.assert_has(errors, "terminal contract")

    def test_policy_history_is_append_only(self) -> None:
        contract = validate_component_evidence.load(validate_component_evidence.CONTRACT_PATH)
        current = validate_component_evidence.load(validate_component_evidence.LOCK_PATH)
        changed = copy.deepcopy(contract)
        changed["contract_version"] = "2.0.1"
        transitioned = copy.deepcopy(current)
        transitioned["locks"].append({
            "policy_version": "component-evidence/2",
            "registry": "contracts/component-evidence.yaml",
            "semantic_sha256": validate_component_evidence.semantic_sha256(changed),
        })
        self.assertEqual(
            [],
            validate_component_evidence.validate(
                changed, transitioned, include_code=False, historical=current,
            ),
        )
        transitioned["locks"][0], transitioned["locks"][1] = \
            transitioned["locks"][1], transitioned["locks"][0]
        self.assert_has(
            validate_component_evidence.validate(
                changed, transitioned, include_code=False, historical=current,
            ),
            "append-only trusted prefix",
        )


if __name__ == "__main__":
    unittest.main()
