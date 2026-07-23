---
name: kansoku-canary-skill
description: A harmless canary skill used only to prove Kansoku's Codex adapter reconciliation chain end to end. It never touches real user data.
version: 1.0.0
---

# kansoku-canary-skill

This skill exists only inside the Kansoku canary fixture project
(`tests/fixtures/session-06/canary/kansoku-canary-fixture-project/`). It is
never installed into a real Codex user configuration and is never loaded
outside a non-interactive, consent-gated, budget-bounded canary run.

## What it does

1. Reads one harmless, generated file (`canary.txt`) from the canary
   workspace.
2. Echoes its contents back through the local `kansoku-canary-echo-mcp`
   MCP server's `echo` tool.

No shell command beyond a bounded read is ever required. This skill never
reads, writes, or references any real user repository or credential.
