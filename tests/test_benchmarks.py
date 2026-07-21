from __future__ import annotations

import hashlib
import json
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
BENCHMARKS = ROOT / "benchmarks" / "session-01"


def load(relative: str):
    return json.loads((BENCHMARKS / relative).read_text(encoding="utf-8"))


class Session01BenchmarkTests(unittest.TestCase):
    def test_backend_results_are_durable_bounded_and_privacy_safe(self) -> None:
        data = load("backend/raw-results.json")
        self.assertEqual({"go", "python"}, {item["implementation"] for item in data["results"]})
        for item in data["results"]:
            self.assertTrue(item["shutdown_flush_verified"])
            self.assertEqual(
                ["body_bytes", "received_at", "record_count", "route", "schema_fingerprint"],
                item["persisted_field_allowlist"],
            )
            self.assertEqual([], item["persisted_row_schema_violations"])
            self.assertEqual([], item["recursive_canary_matches"])
            self.assertEqual(5, len(item["canary_sha256"]))
            self.assertEqual(2, len(item["retained_sanitized_row_evidence"]))
            for row in item["retained_sanitized_row_evidence"]:
                self.assertEqual(set(item["persisted_field_allowlist"]), set(row))
                self.assertEqual("otlp_http_json_logs", row["route"])
                self.assertEqual(5, row["record_count"])
                self.assertEqual("spike.otlp-json-safe-counts/1", row["schema_fingerprint"])
            self.assertEqual(item["expected_batches_including_warmup"], item["persisted_batches"])
            self.assertEqual(item["expected_records_including_warmup"], item["persisted_records"])
            self.assertLess(item["latency_p95_seconds"], 10)
            self.assertEqual(413, item["rejection_statuses"]["oversized_body"])
            alias_statuses = {
                key: value for key, value in item["rejection_statuses"].items()
                if key.startswith("content_alias_")
            }
            self.assertEqual(10, len(alias_statuses))
            self.assertEqual({400}, set(alias_statuses.values()))
        self.assertIn("go1.26", data["toolchains"]["go"]["version"])
        self.assertIn("Python 3.14", data["toolchains"]["python"]["version"])

    def test_database_results_reconcile_and_meet_provisional_query_budget(self) -> None:
        data = load("database/raw-results.json")
        expected = {
            ("sqlite", "personal_sample"),
            ("sqlite", "enthusiast_sample"),
            ("postgresql", "personal_sample"),
            ("postgresql", "enthusiast_sample"),
        }
        self.assertEqual(expected, {(item["engine"], item["profile"]) for item in data["results"]})
        for item in data["results"]:
            self.assertTrue(item["idempotency_verified"])
            self.assertEqual(item["events"], item["rows_after_replay"])
            self.assertLess(max(item["query_seconds"].values()), 0.5)
            if item["engine"] == "sqlite":
                self.assertEqual(0, item["concurrency"]["errors"])
                self.assertGreater(item["concurrency"]["writes"], 0)
            else:
                self.assertIn("0 (0.000%)", "\n".join(item["concurrency"]["raw"]))
        self.assertEqual("percentile_cont", data["quantile_semantics"]["method"])
        for profile in ("personal_sample", "enthusiast_sample"):
            values = [
                item["duration_p95_value"] for item in data["results"]
                if item["profile"] == profile
            ]
            self.assertEqual(2, len(values))
            self.assertAlmostEqual(values[0], values[1])
        self.assertEqual(2, len(data["stress_projections"]))
        self.assertTrue(all("projection" in item["warning"].lower() for item in data["stress_projections"]))

    def test_frontend_results_preserve_accessible_alternatives_and_bundle_evidence(self) -> None:
        data = load("frontend/raw-results.json")
        self.assertEqual({"echarts", "uplot"}, set(data["results"]))
        for item in data["results"].values():
            self.assertTrue(all(item["accessibility_surface"].values()))
            self.assertGreater(item["bundle"]["javascript_gzip_bytes"], 0)
        self.assertLess(data["results"]["uplot"]["bundle"]["total_gzip_bytes"], data["results"]["echarts"]["bundle"]["total_gzip_bytes"])
        self.assertLessEqual(data["results"]["echarts"]["bundle"]["total_gzip_bytes"], 250 * 1024)
        package = json.loads((BENCHMARKS / "frontend" / "package.json").read_text(encoding="utf-8"))
        self.assertEqual(data["resolved_dependencies"], package["dependencies"])
        self.assertEqual(0, data["npm_audit"]["exit_code"])
        self.assertEqual(0, data["npm_audit"]["vulnerabilities"]["total"])

    def test_manifest_hashes_the_final_sanitized_artifacts(self) -> None:
        manifest = load("run-manifest.json")
        self.assertEqual("full_run", manifest["mode"])
        self.assertEqual(3, len(manifest["runs"]))
        self.assertEqual(
            {"backend/run_benchmark.py", "database/run_benchmark.py", "frontend/run_benchmark.py"},
            {item["script"] for item in manifest["runs"]},
        )
        for artifact in manifest["artifacts"]:
            path = ROOT / artifact["path"]
            self.assertEqual(artifact["sha256"], hashlib.sha256(path.read_bytes()).hexdigest())
            text = path.read_text(encoding="utf-8")
            self.assertNotIn("/Users/", text)

    def test_benchmark_external_inputs_are_immutable(self) -> None:
        backend = load("backend/raw-results.json")
        database = load("database/raw-results.json")
        frontend = load("frontend/raw-results.json")
        for reference in backend["immutable_inputs"].values():
            self.assertIn("@sha256:", reference)
        self.assertIn("@sha256:", database["immutable_inputs"]["postgres"])
        self.assertEqual(64, len(frontend["immutable_inputs"]["package_lock_sha256"]))
        source_paths = [
            BENCHMARKS / "backend" / "go" / "Dockerfile",
            BENCHMARKS / "backend" / "python" / "Dockerfile",
            BENCHMARKS / "backend" / "run_benchmark.py",
            BENCHMARKS / "database" / "run_benchmark.py",
            BENCHMARKS / "frontend" / "run_benchmark.py",
        ]
        source = "\n".join(path.read_text(encoding="utf-8") for path in source_paths)
        self.assertNotIn(":latest", source)


if __name__ == "__main__":
    unittest.main()
