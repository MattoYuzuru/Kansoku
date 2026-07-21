from __future__ import annotations

import copy
import hashlib
import shutil
import sqlite3
import sys
import tempfile
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / "scripts"))

import validate_contracts  # noqa: E402


class Session01ContractTests(unittest.TestCase):
    ARTIFACT_PATHS = {
        "capability_contract": "capability-contract.json",
        "privacy_test": "privacy-test.json",
        "sanitized_fixture_replay": "sanitized-replay.json",
        "passive_audit": "passive-audit.json",
        "canary_or_end_to_end": "canary.json",
        "classification_fixture": "classification.json",
    }

    def assert_valid(self, validator) -> None:
        self.assertEqual([], validator())

    def public_claim(self, label: str = "supported"):
        data = validate_contracts.registry("capabilities.yaml")
        mutated = copy.deepcopy(data)
        agent = mutated["agent_evidence_baseline"][0]
        claim = agent["capabilities"][0]
        version_range = {
            "scheme": "semver_core",
            "min_inclusive": "0.144.6",
            "max_exclusive": "0.145.0",
        }
        receipt_ids = []
        receipts = []
        artifacts = []
        for kind in mutated["support_governance"]["required_evidence_kinds"]:
            receipt_id = f"receipt/{kind}-v1"
            relative_path = (
                "tests/fixtures/session-01/evidence-artifacts/" + self.ARTIFACT_PATHS[kind]
            )
            digest = hashlib.sha256((ROOT / relative_path).read_bytes()).hexdigest()
            artifact_id = f"sha256:{digest}"
            receipt_ids.append(receipt_id)
            artifacts.append({
                "artifact_id": artifact_id,
                "kind": kind,
                "path": relative_path,
                "canonicalization": "canonical_json_v1",
                "sha256": digest,
            })
            receipts.append({
                "receipt_id": receipt_id,
                "kind": kind,
                "adapter_id": agent["adapter_id"],
                "capability_id": claim["capability_id"],
                "version_range": copy.deepcopy(version_range),
                "artifact_ids": [artifact_id],
                "result": "pass",
            })
        fixture_path = (
            "tests/fixtures/session-01/evidence-artifacts/"
            + self.ARTIFACT_PATHS["classification_fixture"]
        )
        fixture_digest = hashlib.sha256((ROOT / fixture_path).read_bytes()).hexdigest()
        fixture_id = f"sha256:{fixture_digest}"
        artifacts.append({
            "artifact_id": fixture_id,
            "kind": "classification_fixture",
            "path": fixture_path,
            "canonicalization": "canonical_json_v1",
            "sha256": fixture_digest,
        })
        mutated["evidence_artifact_registry"] = artifacts
        reviews = []
        for suffix in ("a", "b"):
            reviews.append({
                "review_id": f"review/classification-{suffix}",
                "reviewer_id": f"reviewer-{suffix}",
                "adapter_id": agent["adapter_id"],
                "capability_id": claim["capability_id"],
                "version_range": copy.deepcopy(version_range),
                "fixture_ids": [fixture_id],
                "evidence_receipt_ids": list(receipt_ids),
                "result": "approved",
            })
        claim["support"] = label
        claim["version_range"] = version_range
        claim["evidence"] = {
            "official_docs": ["https://developers.openai.com/codex/codex-manual.md"],
            "receipts": receipts,
            "human_classification_reviews": reviews,
        }
        return mutated, claim

    def test_glossary_distinguishes_required_states(self) -> None:
        self.assert_valid(validate_contracts.validate_glossary)

    def test_lifecycle_classification_is_deterministic(self) -> None:
        self.assert_valid(validate_contracts.validate_lifecycle)

    def test_support_claims_require_evidence(self) -> None:
        self.assert_valid(validate_contracts.validate_capabilities)

    def test_supported_and_beta_mutations_cannot_bypass_governance(self) -> None:
        data = validate_contracts.registry("capabilities.yaml")
        for label in ("supported", "beta"):
            mutated = copy.deepcopy(data)
            claim = mutated["agent_evidence_baseline"][0]["capabilities"][0]
            claim["support"] = label
            errors = validate_contracts.validate_capability_data(mutated)
            self.assertTrue(any("version range must contain" in error for error in errors))
            self.assertTrue(any("public evidence must use" in error for error in errors))

    def test_well_bound_public_claim_receipts_validate(self) -> None:
        for label in ("supported", "beta"):
            mutated, _claim = self.public_claim(label)
            self.assertEqual([], validate_contracts.validate_capability_data(mutated))

    def test_public_claim_rejects_banana_later_and_reversed_semver(self) -> None:
        mutated, claim = self.public_claim()
        claim["version_range"] = {"scheme": "semver_core", "min_inclusive": "banana", "max_exclusive": "later"}
        errors = validate_contracts.validate_capability_data(mutated)
        self.assertTrue(any("invalid min_inclusive" in error for error in errors))
        self.assertTrue(any("invalid max_exclusive" in error for error in errors))

        mutated, claim = self.public_claim()
        reversed_range = {"scheme": "semver_core", "min_inclusive": "0.145.0", "max_exclusive": "0.144.6"}
        claim["version_range"] = reversed_range
        for receipt in claim["evidence"]["receipts"]:
            receipt["version_range"] = copy.deepcopy(reversed_range)
        for review in claim["evidence"]["human_classification_reviews"]:
            review["version_range"] = copy.deepcopy(reversed_range)
        errors = validate_contracts.validate_capability_data(mutated)
        self.assertTrue(any("version range must be ordered" in error for error in errors))

    def test_public_claim_rejects_untyped_evidence_and_mismatched_review_binding(self) -> None:
        mutated, claim = self.public_claim()
        claim["evidence"]["receipts"][0]["artifact_ids"] = "arbitrary prose"
        errors = validate_contracts.validate_capability_data(mutated)
        self.assertTrue(any("artifact_ids must be non-empty bounded identifiers" in error for error in errors))

        mutated, claim = self.public_claim()
        claim["evidence"]["receipts"][0]["artifact_ids"] = ["artifact/arbitrary-v1"]
        errors = validate_contracts.validate_capability_data(mutated)
        self.assertTrue(any("unresolved evidence artifact" in error for error in errors))

        mutated, _claim = self.public_claim()
        mutated["evidence_artifact_registry"][0] = "arbitrary evidence"
        errors = validate_contracts.validate_capability_data(mutated)
        self.assertTrue(any("every entry must be a typed mapping" in error for error in errors))

        mutated, claim = self.public_claim()
        review = claim["evidence"]["human_classification_reviews"][0]
        review["capability_id"] = "model.usage"
        review["evidence_receipt_ids"] = ["receipt/not-the-claim"]
        errors = validate_contracts.validate_capability_data(mutated)
        self.assertTrue(any("adapter/capability binding mismatch" in error for error in errors))
        self.assertTrue(any("must cite the exact claim receipts" in error for error in errors))

    def test_evidence_artifacts_verify_bytes_paths_and_content_addresses(self) -> None:
        normal_path = ROOT / "tests/fixtures/session-01/evidence-artifacts/capability-contract.json"
        normal_bytes = normal_path.read_bytes()
        canonical, payload = validate_contracts.canonical_json_v1(normal_bytes)
        self.assertEqual(normal_bytes, canonical)
        self.assertEqual("capability_contract", payload["kind"])

        mutated, claim = self.public_claim()
        artifact = mutated["evidence_artifact_registry"][0]

        artifact["sha256"] = "0" * 64
        artifact["artifact_id"] = "sha256:" + "0" * 64
        claim["evidence"]["receipts"][0]["artifact_ids"] = [artifact["artifact_id"]]
        errors = validate_contracts.validate_capability_data(mutated)
        self.assertTrue(any("SHA-256 mismatch" in error for error in errors))

        mutated, claim = self.public_claim()
        artifact = mutated["evidence_artifact_registry"][0]
        artifact["artifact_id"] = "artifact/fabricated-v1"
        claim["evidence"]["receipts"][0]["artifact_ids"] = [artifact["artifact_id"]]
        errors = validate_contracts.validate_capability_data(mutated)
        self.assertTrue(any("not its content address" in error for error in errors))

        for bad_path, marker in (
            ("contracts/capabilities.yaml", "outside allowed root"),
            ("tests/fixtures/session-01/evidence-artifacts/../formula-cases.yaml", "unsafe relative path"),
            ("tests/fixtures/session-01/evidence-artifacts/missing.json", "missing file or symlink escape"),
        ):
            mutated, _claim = self.public_claim()
            mutated["evidence_artifact_registry"][0]["path"] = bad_path
            errors = validate_contracts.validate_capability_data(mutated)
            self.assertTrue(any(marker in error for error in errors), (bad_path, errors))

        with tempfile.TemporaryDirectory() as temporary:
            temporary_root = Path(temporary)
            fixture_root = temporary_root / "tests/fixtures/session-01/evidence-artifacts"
            shutil.copytree(ROOT / "tests/fixtures/session-01/evidence-artifacts", fixture_root)
            mutated, _claim = self.public_claim()
            changed_path = fixture_root / self.ARTIFACT_PATHS["capability_contract"]
            changed_path.write_bytes(
                b'{"artifact_schema":"kansoku.synthetic-evidence/1","kind":"capability_contract",'
                b'"summary":"altered but sanitized fixture","synthetic":true}\n'
            )
            errors = validate_contracts.validate_capability_data(mutated, artifact_root=temporary_root)
            self.assertTrue(any("SHA-256 mismatch" in error for error in errors))

            changed_path.write_bytes(
                (ROOT / "tests/fixtures/session-01/evidence-artifacts/capability-contract.json").read_bytes()
                + b"\n"
            )
            mutated, _claim = self.public_claim()
            errors = validate_contracts.validate_capability_data(mutated, artifact_root=temporary_root)
            self.assertTrue(any("non-canonical JSON bytes" in error for error in errors))

            outside = temporary_root / "outside.json"
            outside.write_bytes(b'{"kind":"capability_contract"}\n')
            symlink_path = fixture_root / "escape.json"
            symlink_path.symlink_to(outside)
            mutated, _claim = self.public_claim()
            mutated["evidence_artifact_registry"][0]["path"] = (
                "tests/fixtures/session-01/evidence-artifacts/escape.json"
            )
            errors = validate_contracts.validate_capability_data(mutated, artifact_root=temporary_root)
            self.assertTrue(any("symlink escape" in error for error in errors))

            for constant in (b"NaN", b"Infinity", b"-Infinity", b"1e400"):
                raw = (
                    b'{"artifact_schema":"kansoku.synthetic-evidence/1",'
                    b'"kind":"capability_contract","measurement":' + constant
                    + b',"summary":"sanitized non-finite regression","synthetic":true}\n'
                )
                changed_path.write_bytes(raw)
                digest = hashlib.sha256(raw).hexdigest()
                mutated, claim = self.public_claim()
                artifact = mutated["evidence_artifact_registry"][0]
                artifact["artifact_id"] = f"sha256:{digest}"
                artifact["sha256"] = digest
                claim["evidence"]["receipts"][0]["artifact_ids"] = [artifact["artifact_id"]]
                errors = validate_contracts.validate_capability_data(
                    mutated, artifact_root=temporary_root
                )
                self.assertTrue(
                    any("non-canonical JSON bytes" in error for error in errors),
                    (constant, errors),
                )

    def test_capability_matrix_mutation_cannot_omit_a_pair_silently(self) -> None:
        data = validate_contracts.registry("capabilities.yaml")
        mutated = copy.deepcopy(data)
        mutated["agent_evidence_baseline"][0]["omitted_capabilities"].pop()
        errors = validate_contracts.validate_capability_data(mutated)
        self.assertTrue(any("capability baseline coverage differs" in error for error in errors))

    def test_metric_formulas_and_references_reconcile(self) -> None:
        self.assert_valid(validate_contracts.validate_metrics)

    def test_metric_registry_expression_and_fixture_population_mutations_fail(self) -> None:
        metrics = validate_contracts.registry("metrics.yaml")
        fixtures = validate_contracts.fixture("formula-cases.yaml")
        mutated_metrics = copy.deepcopy(metrics)
        mutated_metrics["metrics"][0]["formula"]["expression"] = "banana arithmetic"
        errors = validate_contracts.validate_metric_data(mutated_metrics, fixtures)
        self.assertTrue(any("registry semantic fingerprint mismatch" in error for error in errors))
        self.assertTrue(any("expression differs from registry" in error for error in errors))

        mutated_fixtures = copy.deepcopy(fixtures)
        mutated_fixtures["metric_cases"][0]["population"] = "unbound population"
        errors = validate_contracts.validate_metric_data(metrics, mutated_fixtures)
        self.assertTrue(any("population differs from registry" in error for error in errors))

    def test_formula_version_locks_enforce_bootstrap_and_append_only_history_modes(self) -> None:
        metrics = validate_contracts.registry("metrics.yaml")
        fixtures = validate_contracts.fixture("formula-cases.yaml")
        locks = validate_contracts.registry("formula-version-locks.yaml")
        mutated_metrics = copy.deepcopy(metrics)
        mutated_fixtures = copy.deepcopy(fixtures)
        mutated_locks = copy.deepcopy(locks)
        metric = mutated_metrics["metrics"][0]
        formula = metric["formula"]
        formula["expression"] = "coherently rewritten semantics under the old version"
        digest = validate_contracts.semantic_sha256(
            validate_contracts.formula_semantic_payload(metric)
        )
        formula["semantic_sha256"] = digest
        fixture_case = mutated_fixtures["metric_cases"][0]
        fixture_case["expression"] = formula["expression"]
        fixture_case["registry_semantic_sha256"] = digest

        errors = validate_contracts.validate_metric_data(
            mutated_metrics, mutated_fixtures, locks, historical_locks=None
        )
        self.assertTrue(any("differs from independent lock" in error for error in errors))

        version = formula["version"]
        lock = next(item for item in mutated_locks["locks"] if item["formula_version"] == version)
        lock["semantic_sha256"] = digest
        self.assertEqual(
            [],
            validate_contracts.validate_metric_data(
                mutated_metrics, mutated_fixtures, mutated_locks, historical_locks=None
            ),
            "bootstrap mode is intentionally review-controlled and deterministic without Git",
        )
        errors = validate_contracts.validate_metric_data(
            mutated_metrics, mutated_fixtures, mutated_locks, historical_locks=locks
        )
        self.assertTrue(any("append-only history changed" in error for error in errors))

        versioned_metrics = copy.deepcopy(mutated_metrics)
        versioned_fixtures = copy.deepcopy(mutated_fixtures)
        versioned_locks = copy.deepcopy(locks)
        metric = versioned_metrics["metrics"][0]
        formula = metric["formula"]
        old_version = formula["version"]
        formula["version"] = old_version.rsplit("/", 1)[0] + "/2"
        formula["population_id"] = metric["id"] + ".population/2"
        formula["evaluator"]["id"] = formula["version"]
        digest = validate_contracts.semantic_sha256(validate_contracts.formula_semantic_payload(metric))
        formula["semantic_sha256"] = digest
        case = versioned_fixtures["metric_cases"][0]
        case["formula_version"] = formula["version"]
        case["population_id"] = formula["population_id"]
        case["evaluator"] = copy.deepcopy(formula["evaluator"])
        case["registry_semantic_sha256"] = digest
        versioned_locks["locks"].append({
            "formula_version": formula["version"],
            "semantic_sha256": digest,
        })
        self.assertEqual(
            [],
            validate_contracts.validate_metric_data(
                versioned_metrics, versioned_fixtures, versioned_locks, historical_locks=locks
            ),
            "a new version is valid only while every historical lock remains unchanged",
        )
        self.assertIsNone(validate_contracts.git_formula_history("refs/kansoku-does-not-exist"))
        with self.assertRaises(ValueError):
            validate_contracts.git_formula_history("refs/kansoku-does-not-exist", required=True)

    def test_formula_evaluator_schema_and_ratio_invariants_reject_semantic_bypass(self) -> None:
        metrics = validate_contracts.registry("metrics.yaml")
        fixtures = validate_contracts.fixture("formula-cases.yaml")
        locks = validate_contracts.registry("formula-version-locks.yaml")

        mutated_metrics = copy.deepcopy(metrics)
        mutated_fixtures = copy.deepcopy(fixtures)
        mutated_locks = copy.deepcopy(locks)
        metric = next(item for item in mutated_metrics["metrics"] if item["formula"]["calculation"] == "sum")
        case = next(item for item in mutated_fixtures["metric_cases"] if item["metric_id"] == metric["id"])
        metric["formula"]["evaluator"]["parameters"] = {"value_field": "numerator"}
        digest = validate_contracts.semantic_sha256(validate_contracts.formula_semantic_payload(metric))
        metric["formula"]["semantic_sha256"] = digest
        case["evaluator"] = copy.deepcopy(metric["formula"]["evaluator"])
        case["fixture_policy"] = copy.deepcopy(metric["formula"]["fixture_policy"])
        case["registry_semantic_sha256"] = digest
        lock = next(
            item for item in mutated_locks["locks"]
            if item["formula_version"] == metric["formula"]["version"]
        )
        lock["semantic_sha256"] = digest
        errors = validate_contracts.validate_metric_data(
            mutated_metrics, mutated_fixtures, mutated_locks, historical_locks=None
        )
        self.assertTrue(any("exact typed schema" in error for error in errors))
        self.assertEqual(
            (None, "unknown", "invalid_evaluator"),
            validate_contracts.evaluate_formula_case(case),
        )

        mutated_metrics = copy.deepcopy(metrics)
        mutated_fixtures = copy.deepcopy(fixtures)
        mutated_locks = copy.deepcopy(locks)
        metric = next(item for item in mutated_metrics["metrics"] if item["formula"]["calculation"] == "ratio")
        case = next(item for item in mutated_fixtures["metric_cases"] if item["metric_id"] == metric["id"])
        metric["formula"]["numerator"] = metric["formula"]["denominator"]
        digest = validate_contracts.semantic_sha256(validate_contracts.formula_semantic_payload(metric))
        metric["formula"]["semantic_sha256"] = digest
        case["registry_semantic_sha256"] = digest
        lock = next(
            item for item in mutated_locks["locks"]
            if item["formula_version"] == metric["formula"]["version"]
        )
        lock["semantic_sha256"] = digest
        errors = validate_contracts.validate_metric_data(
            mutated_metrics, mutated_fixtures, mutated_locks, historical_locks=None
        )
        self.assertTrue(any("numerator and denominator must be distinct" in error for error in errors))

    def test_metric_fixture_rejects_unauthorized_exclusion_and_uses_percentile_cont(self) -> None:
        self.assertAlmostEqual(3.85, validate_contracts.percentile_cont([1, 2, 3, 4], 0.95))
        case = {
            "calculation": "sum",
            "dedupe_key": "record_id",
            "authorized_exclusions": [],
            "records": [{
                "record_id": "a", "value": 1, "eligible": True, "in_interval": True,
                "completeness_state": "complete", "exclusion_code": "quiet_period",
            }],
        }
        self.assertEqual(
            (None, "unknown", "unauthorized_exclusion"),
            validate_contracts.evaluate_formula_case(case),
        )

    def test_every_panel_has_registered_metrics_and_states(self) -> None:
        self.assert_valid(validate_contracts.validate_dashboard)
        dashboard = validate_contracts.registry("dashboard.yaml")
        expected = validate_contracts.registry("glossary.yaml")["state_registry"]["display_states"]
        for route in dashboard["routes"]:
            for panel in route["panels"]:
                self.assertEqual(expected, panel["view_states"])
                self.assertNotIn("zero", panel["view_states"])

    def test_every_slo_query_runs_against_a_test_load(self) -> None:
        self.assert_valid(validate_contracts.validate_slos)

    def test_missing_raw_sink_evidence_fails_instead_of_becoming_zero(self) -> None:
        data = validate_contracts.registry("slo.yaml")
        samples = validate_contracts.fixture("slo-samples.yaml")
        mutated = copy.deepcopy(samples)
        del mutated["passing_cases"]["raw-content-persisted-count"]["scope_values"]["backups"]
        errors = validate_contracts.validate_slo_data(data, mutated)
        self.assertTrue(any("raw-content-persisted-count" in error for error in errors))

    def test_ineligible_backup_scope_and_authorized_scope_exclusion_cannot_pass(self) -> None:
        data = validate_contracts.registry("slo.yaml")
        raw_slo = next(item for item in data["slos"] if item["id"] == "raw-content-persisted-count")
        samples = validate_contracts.fixture("slo-samples.yaml")
        records = validate_contracts.passing_slo_records(
            raw_slo["id"], samples["passing_cases"][raw_slo["id"]]
        )
        backup = next(item for item in records if item["evidence_scope"] == "backups")
        backup["eligible"] = False
        conn = sqlite3.connect(":memory:")
        conn.execute(
            "CREATE TABLE sli_samples (slo_id TEXT, value REAL, eligible INTEGER, "
            "completeness_state TEXT, exclusion_code TEXT, evidence_scope TEXT, scope_exclusion INTEGER)"
        )
        outcome = validate_contracts.evaluate_slo_records(conn, raw_slo, records)
        self.assertEqual("missing", outcome["required_scope_status"]["backups"])
        self.assertEqual(("unknown", "fail"), (outcome["measurement_state"], outcome["gate"]))

        latency_slo = next(item for item in data["slos"] if item["id"] == "live-ingest-latency-p95")
        outcome = validate_contracts.evaluate_slo_records(conn, latency_slo, [{
            "value": 0, "eligible": False, "completeness_state": "unknown",
            "exclusion_code": "declared_maintenance", "evidence_scope": "primary",
            "scope_exclusion": True,
        }])
        conn.close()
        self.assertEqual("excluded", outcome["required_scope_status"]["primary"])
        self.assertEqual({"declared_maintenance": 1}, outcome["authorized_exclusion_counts"])
        self.assertEqual(("partial", "fail"), (outcome["measurement_state"], outcome["gate"]))

    def test_documents_reference_implemented_contracts(self) -> None:
        self.assert_valid(validate_contracts.validate_documentation)


if __name__ == "__main__":
    unittest.main()
