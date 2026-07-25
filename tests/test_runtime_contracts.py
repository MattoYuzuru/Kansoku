from __future__ import annotations

import copy
import sys
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT))
from scripts import validate_runtime  # noqa: E402


class Session09RuntimeContractTests(unittest.TestCase):
    def assert_has(self, errors: list[str], fragment: str) -> None:
        self.assertTrue(any(fragment in error for error in errors), errors)

    def coherent(self, data: dict) -> dict:
        return validate_runtime.coherent_locks(data)

    def test_contracts_and_locks_validate_without_code(self) -> None:
        self.assertEqual([], validate_runtime.validate(include_code=False))

    def test_registry_set_is_exact(self) -> None:
        data = validate_runtime.registries()
        data.pop(next(iter(data)))
        self.assert_has(validate_runtime.validate(data, include_code=False), "registry set is not exact")

    def test_coherent_lock_cannot_publish_database_port(self) -> None:
        data = copy.deepcopy(validate_runtime.registries())
        data["contracts/runtime/runtime-and-api.yaml"]["listeners"]["database_host_port_published"] = True
        self.assert_has(validate_runtime.validate(data, self.coherent(data), include_code=False), "listener loopback/database")

    def test_coherent_lock_cannot_collapse_secrets(self) -> None:
        data = copy.deepcopy(validate_runtime.registries())
        auth = data["contracts/runtime/auth-and-plans.yaml"]
        auth["route_authorization"]["read"]["credential"] = "ingress_bearer"
        self.assert_has(validate_runtime.validate(data, self.coherent(data), include_code=False), "authorization separation")

    def test_coherent_lock_cannot_remove_csrf(self) -> None:
        data = copy.deepcopy(validate_runtime.registries())
        data["contracts/runtime/auth-and-plans.yaml"]["route_authorization"]["mutation"]["csrf"] = False
        self.assert_has(validate_runtime.validate(data, self.coherent(data), include_code=False), "authorization separation")

    def test_coherent_lock_cannot_weaken_plan_binding(self) -> None:
        data = copy.deepcopy(validate_runtime.registries())
        data["contracts/runtime/auth-and-plans.yaml"]["plan_apply"]["approval_binding"].remove("original_sha256")
        self.assert_has(validate_runtime.validate(data, self.coherent(data), include_code=False), "plan approval binding")

    def test_coherent_lock_cannot_ack_filestore_only(self) -> None:
        data = copy.deepcopy(validate_runtime.registries())
        ack = data["contracts/runtime/queue-and-durability.yaml"]["acknowledgement"]
        ack["filestore_alone_is_not_production_acknowledgement"] = False
        self.assert_has(validate_runtime.validate(data, self.coherent(data), include_code=False), "durable acknowledgement")

    def test_coherent_lock_cannot_remove_preaccept_reservation(self) -> None:
        data = copy.deepcopy(validate_runtime.registries())
        data["contracts/runtime/queue-and-durability.yaml"]["admission"]["reservation_before_filestore_acceptance"] = False
        self.assert_has(validate_runtime.validate(data, self.coherent(data), include_code=False), "pre-acceptance")

    def test_coherent_lock_cannot_make_lanes_global(self) -> None:
        data = copy.deepcopy(validate_runtime.registries())
        data["contracts/runtime/queue-and-durability.yaml"]["lanes"][0]["capacity"] = 1024
        self.assert_has(validate_runtime.validate(data, self.coherent(data), include_code=False), "lane/capacity")

    def test_coherent_lock_cannot_drop_integrity_backup(self) -> None:
        data = copy.deepcopy(validate_runtime.registries())
        covered = data["contracts/runtime/operations-backup-and-soak.yaml"]["native_backup"]["covered_table_groups"]
        covered["integrity"].remove("integrity_audit_reports")
        self.assert_has(validate_runtime.validate(data, self.coherent(data), include_code=False), "backup coverage")

    def test_coherent_lock_cannot_trust_exported_formula(self) -> None:
        data = copy.deepcopy(validate_runtime.registries())
        boundary = data["contracts/runtime/operations-backup-and-soak.yaml"]["portable_export_import"]
        boundary["import_never_trusts"].remove("formula_definition")
        self.assert_has(validate_runtime.validate(data, self.coherent(data), include_code=False), "import trust boundary")

    def test_coherent_lock_cannot_add_diagnostics_paths(self) -> None:
        data = copy.deepcopy(validate_runtime.registries())
        data["contracts/runtime/operations-backup-and-soak.yaml"]["diagnostics"]["forbidden"].remove("paths")
        self.assert_has(validate_runtime.validate(data, self.coherent(data), include_code=False), "diagnostics privacy")

    def test_coherent_lock_cannot_claim_wall_clock_soak(self) -> None:
        data = copy.deepcopy(validate_runtime.registries())
        data["contracts/runtime/operations-backup-and-soak.yaml"]["accelerated_soak"]["wall_clock_seven_day_claim"] = True
        self.assert_has(validate_runtime.validate(data, self.coherent(data), include_code=False), "soak evidence scope")

    def test_lock_history_is_append_only(self) -> None:
        current = validate_runtime.load(validate_runtime.LOCK_PATH)
        reordered = copy.deepcopy(current)
        reordered["locks"][0], reordered["locks"][1] = reordered["locks"][1], reordered["locks"][0]
        self.assert_has(
            validate_runtime.validate(validate_runtime.registries(), reordered, include_code=False, historical=current),
            "append-only trusted prefix",
        )


if __name__ == "__main__":
    unittest.main()
