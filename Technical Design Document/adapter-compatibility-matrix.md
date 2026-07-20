# Adapter compatibility matrix

Legend: **N** native documented signal, **R** deterministic reconstruction, **I** inference,
**P** planned/needs fixtures, **—** unsupported or not yet established. This matrix is a planning
baseline, not a support claim; live evidence and version-specific manifests are authoritative.

| Capability | Codex | Claude Code | Gemini CLI | Cursor | Generic adapter |
|---|---:|---:|---:|---:|---:|
| Agent/version discovery | N/R | N/R | N/R | P | required |
| Session lifecycle | N/R | N/R | N | P | optional |
| Prompt count/size | N/R | N/R | N | P | optional |
| Prompt content excluded | configurable | configurable | `logPrompts=false` | P | required policy |
| Model/token metadata | N | N | N | P | optional |
| Explicit skill invocation | R/N when exposed | N (`Skill`) | P | P | optional |
| Implicit skill activation | R/I | N/R/I | P/I | P/I | optional |
| Plugin attribution | inventory/R | N/R | extension mapping | P | optional |
| MCP server/tool usage | N/R | N | N | P | optional |
| Tool calls/latency | N | N | N | P | optional |
| Hook integration | N | N | N | documented/P | optional |
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
agent_versions: ">=x <y"
capabilities:
  skill_invocation:
    support: supported
    evidence: [otel, transcript_skill_tool]
    fixtures: [claude-x-skill.jsonl]
    canary: claude-skill-v1
```

No wildcard “all future versions” support is allowed.

