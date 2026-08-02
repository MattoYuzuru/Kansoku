# Adapter compatibility matrix

Legend: **N** native documented signal, **R** deterministic reconstruction, **I** inference,
**P** planned/needs fixtures, **—** unsupported or not yet established. This matrix is a planning
baseline, not a support claim; live evidence and version-specific manifests are authoritative.

An **—** in the exposure row is a declared `unsupported` plane, not a missing measurement: the agent
publishes no model-visible skill set, so the appliance renders `unsupported` rather than zero and
falls back to inventory completeness for cold eligibility. See TDD 14 and ADR 0023.

| Capability | Codex | Claude Code | Gemini CLI | Cursor | Generic adapter |
|---|---:|---:|---:|---:|---:|
| Agent/version discovery | N/R | N/R | N/R | P | required |
| Session lifecycle | N/R | N/R | N | N/P via hooks | optional |
| Prompt count/size | N/R | N/R | N | N/P via hooks | optional |
| Prompt content excluded | configurable | configurable | `logPrompts=false` | P | required policy |
| Model/token metadata | N | N | N | P | optional |
| Explicit skill invocation | R/N when exposed | N (`Skill`) | P | P | optional |
| Implicit skill activation | R/I | N/R/I | P/I | P/I | optional |
| Skill exposure plane | N (`skills/list`) | — declared unsupported | — | — | optional |
| Plugin attribution | inventory/R | N/R | extension mapping | P | optional |
| MCP server/tool usage | N/R | N | N | N/P via hooks | optional |
| Tool calls/latency | N | N | N | N/P via hooks | optional |
| Hook integration | N | N | N | N/P | optional |
| Native OTel | N | N | N | —/P | optional |
| Historical transcript import | R | R | P | P | optional |
| Component inventory | N/R | N/R | N/R | N/R | required for inventory adapters |
| Live canary | P | P | P | P | optional |

## Support labels

- **Supported:** capability contract, privacy tests, fixtures, passive audit and live or equivalent
  end-to-end verification pass for declared versions.
- **Beta:** deterministic data path exists, but live/version coverage is incomplete.
- **Experimental:** inventory or source probe exists; no completeness promise.
- **Unsupported:** no reliable source or explicitly out of scope.

The dashboard shows labels per capability, not one label for the entire agent.

## Version manifest requirements

Each released adapter matrix row expands into machine-readable records:

```yaml
adapter: claude-code
adapter_version: 0.1.0
evidence_artifact_registry:
  - artifact_id: "sha256:<verified-contract-digest>"
    kind: capability_contract
    path: tests/fixtures/claude-code/component-lifecycle-contract.json
    canonicalization: canonical_json_v1
    sha256: "<verified-contract-digest>"
capabilities:
  skill_invocation:
    support: supported
    version_range: &range
      {scheme: semver_core, min_inclusive: 2.1.197, max_exclusive: 2.2.0}
    evidence:
      official_docs: [https://code.claude.com/docs/en/monitoring-usage]
      receipts: &receipts
        - {receipt_id: receipt/contract-v1, kind: capability_contract, adapter_id: claude-code, capability_id: component.lifecycle, version_range: *range, artifact_ids: ["sha256:<verified-contract-digest>"], result: pass}
        - {receipt_id: receipt/privacy-v1, kind: privacy_test, adapter_id: claude-code, capability_id: component.lifecycle, version_range: *range, artifact_ids: ["sha256:<verified-privacy-digest>"], result: pass}
        - {receipt_id: receipt/replay-v1, kind: sanitized_fixture_replay, adapter_id: claude-code, capability_id: component.lifecycle, version_range: *range, artifact_ids: ["sha256:<verified-replay-digest>"], result: pass}
        - {receipt_id: receipt/audit-v1, kind: passive_audit, adapter_id: claude-code, capability_id: component.lifecycle, version_range: *range, artifact_ids: ["sha256:<verified-audit-digest>"], result: pass}
        - {receipt_id: receipt/canary-v1, kind: canary_or_end_to_end, adapter_id: claude-code, capability_id: component.lifecycle, version_range: *range, artifact_ids: ["sha256:<verified-canary-digest>"], result: pass}
      human_classification_reviews:
        - {review_id: review/classification-a, reviewer_id: reviewer-a, adapter_id: claude-code, capability_id: component.lifecycle, version_range: *range, fixture_ids: ["sha256:<verified-classification-digest>"], evidence_receipt_ids: [receipt/contract-v1, receipt/privacy-v1, receipt/replay-v1, receipt/audit-v1, receipt/canary-v1], result: approved}
        - {review_id: review/classification-b, reviewer_id: reviewer-b, adapter_id: claude-code, capability_id: component.lifecycle, version_range: *range, fixture_ids: ["sha256:<verified-classification-digest>"], evidence_receipt_ids: [receipt/contract-v1, receipt/privacy-v1, receipt/replay-v1, receipt/audit-v1, receipt/canary-v1], result: approved}
```

The digest placeholders above describe schema shape and are not valid evidence. The YAML anchor only
shortens this documentation example; every stored receipt carries the complete range. Every real
artifact/fixture ID must equal `sha256:<digest>` and resolve in `evidence_artifact_registry` to a
canonical JSON file under `tests/fixtures`. The validator rejects unsafe/missing paths and symlink
escape, verifies canonical bytes and payload kind, and recomputes the digest; a registry string or
declared hash alone is not evidence. No prose range,
reversed bound or wildcard “all future versions” support is allowed. Version schemes are explicit;
Session 01 implements strict numeric SemVer core comparison and requires a new parser/comparator
before another scheme can be registered. Supported and Beta are both public claims and must satisfy
the typed, exactly bound privacy/replay/audit/end-to-end and two-independent-human-receipt gate in
`contracts/capabilities.yaml`; Beta only communicates disclosed coverage limits after those gates.

## Session 01 support state

The signal table above describes documented or reconstructable source availability, not released
support. `contracts/capabilities.yaml` is authoritative for current claims: documentation-only rows
are **Experimental**, and absent stable sources are **Unsupported**. No adapter is **Supported** or
**Beta** until its privacy tests, sanitized fixtures, bounded version manifest, passive audit and
required end-to-end evidence exist.
