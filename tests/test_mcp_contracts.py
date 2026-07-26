import subprocess
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]

class MCPContractTests(unittest.TestCase):
    def test_contract_validator(self):
        result = subprocess.run(
            ["python3", "scripts/validate_mcp.py"], cwd=ROOT,
            text=True, capture_output=True,
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("passed", result.stdout)

    def test_dashboard_never_labels_mcp_protocol_completion_as_user_success(self):
        source = (ROOT / "web/src/pages/MCP.tsx").read_text()
        self.assertNotIn('label="Succeeded"', source)

if __name__ == "__main__":
    unittest.main()
