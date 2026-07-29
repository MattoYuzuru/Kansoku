# Artifact manifest — 2026-07-30

Schema: `kansoku.defect-research-artifacts/1`

## Privacy boundary

The package contains only safe aggregate counts, opaque local pseudonyms, bounded error classes,
DOM/layout observations, screenshots, source-code locators, and public version information. It
does not retain raw prompts, model responses, tool input/output, credentials, environment values,
or user-specific host/telemetry filesystem paths. The reproducible harness contains only standard
browser executable candidates and accepts an explicit executable override.

## SHA-256

```text
589f0f5d02d3881f13c4c9827186218c19ab67d53ca60afca1d86b4e1a9fcf82  reports/2026-07-30-defect-research-and-priority-plan.md
806de0c60f08ea94ddaa32e032bef08220297a02869eb8d086205cf2b28fa8d6  reports/2026-07-30-defect-inventory.json
8704a87f3be255f86e34bcdb82857dad19e32bebabe83e8d88e2171fdcc7a8e7  reports/2026-07-30-next-agent-prompt.md
8c23857aeab7dbf25957b5ae686b1c82ea9004e8f906131500baa00f1740d103  reports/artifacts/2026-07-30/browser-evidence.json
8a47fcbff3a75862c5b81aeda3a752c67957a67c749209d5e0bbcf957931dcb5  reports/artifacts/2026-07-30/live-evidence.json
7d90265a808f7a1e257016d7c28438c1e53e0789ad93ea6ca804d23adca67384  reports/artifacts/2026-07-30/browser-research.mjs
8ce604d81364ba806b048a8a942caede8e1409fa2e1981fb51449f015cd86367  reports/artifacts/2026-07-30/agent-profile.png
cb179214c65a523595c91c0c6c08447e71afe46d3f23d22201385fbc9fb10695  reports/artifacts/2026-07-30/reliability-incidents.png
7573b384eb98172348ea6f4082bdb0fb5174b75ad0e7623bfbf2be13b9e008d8  reports/artifacts/2026-07-30/skill-profile-after-spa-click.png
473f828165be8cb7be2117c33e2fc7a0112603c714240fe40acbf3d89377c469  reports/artifacts/2026-07-30/system-mobile.png
28157858c979bc50d4f40781d3447cad4b84643a2d4a96b1128b1ef8dca4b2a3  reports/artifacts/2026-07-30/system-tablet.png
```

## Reproduction and validation

Executed successfully:

```text
node reports/artifacts/2026-07-30/browser-research.mjs
jq empty reports/2026-07-30-defect-inventory.json \
  reports/artifacts/2026-07-30/browser-evidence.json \
  reports/artifacts/2026-07-30/live-evidence.json
node --check reports/artifacts/2026-07-30/browser-research.mjs
python3 scripts/validate_contracts.py
python3 scripts/validate_data_platform.py
git diff --check
```

The browser run used an ephemeral Chrome profile and observed:

- one skill-profile render exception;
- five HTTP 503 agent-profile responses;
- range reset from 7 to 30 days;
- a full document reload on Reliability tab navigation;
- one overflowing KPI on both Reliability and System at desktop, tablet, and mobile widths;
- two native Reliability selects and one visible Next page link;
- no transport-level failed requests.
