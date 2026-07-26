import copy
import importlib.util
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
SPEC = importlib.util.spec_from_file_location(
    "validate_plugins", ROOT / "scripts" / "validate_plugins.py",
)
validate_plugins = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(validate_plugins)

class PluginContractTests(unittest.TestCase):
    def test_contract_validator(self):
        validate_plugins.main()

    def test_active_share_population_excludes_incomplete_graph(self):
        metrics = validate_plugins.load(
            ROOT / "contracts" / "plugins" / "metrics-and-privacy.yaml",
        )
        active = metrics["formulas"]["plugin.active_share/1"]
        self.assertIn("complete", active["denominator"])
        self.assertIn("incomplete_enabled_or_child_graph", active["exclusions"])

    def test_success_and_content_are_not_fabricated(self):
        evidence = validate_plugins.load(
            ROOT / "contracts" / "plugins" / "evidence-and-attribution.yaml",
        )
        metrics = validate_plugins.load(
            ROOT / "contracts" / "plugins" / "metrics-and-privacy.yaml",
        )
        self.assertIn("unsupported", evidence["success_rule"])
        self.assertFalse(metrics["content_endpoint"])

if __name__ == "__main__":
    unittest.main()
