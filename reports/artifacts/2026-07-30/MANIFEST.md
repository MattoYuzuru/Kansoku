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
3cbeac3c9985d44c8660427c7a18d3ca039a365472cd5cbb3e25b0a49a42357a  reports/artifacts/2026-07-30/browser-evidence.json
8a47fcbff3a75862c5b81aeda3a752c67957a67c749209d5e0bbcf957931dcb5  reports/artifacts/2026-07-30/live-evidence.json
41832564f2d1c229b769bad859cdbb20f9a2a315a49416827d20b905fccd27f5  reports/artifacts/2026-07-30/browser-research.mjs
0fc81a40de7bb3385e958244b499fc87ad59ad48d6bc2e48a921901926ecbb41  reports/artifacts/2026-07-30/agent-profile.png
f849e183d2ab0551f18ef0a0ca817ce94de9492efcc31c8a5020d7a9ab98d52d  reports/artifacts/2026-07-30/reliability-incidents.png
00cac55fa76de0891425d1791141fe83d34037019b8f25c3736b6872192f5903  reports/artifacts/2026-07-30/skill-profile-after-spa-click.png
b0d5f460333c7c86d73efd5848d7b20283fe5aef8c99fa6966d3a877ba6b739e  reports/artifacts/2026-07-30/system-mobile.png
1ba96d3ed6a2f2fcb49cd6e6f86c0b4b7ba1f60ef1e1797fa3f04f3d84fc2d72  reports/artifacts/2026-07-30/system-tablet.png
a8706997b950f987ea7c6a14ba914e6f271988c20b9507cc91481da9c02968ae  reports/artifacts/2026-07-30/system-zoom_200.png
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

- 200 responses for all five agent profiles and a rendered skill profile without exceptions;
- independent Activity=7d and Models=12-month preferences across SPA navigation and reload;
- weighted Models error ratio with formula/population/exclusion text;
- separate collection receive-to-commit, observation-age, replay, late/backfill and clock-skew
  states;
- SPA Reliability tab navigation, Back and refresh with 25 incident rows, a Load more fallback,
  zero native selects and zero legacy Next page links;
- zero overflow findings at desktop, tablet, mobile and 200% zoom;
- zero runtime exceptions, transport failures and non-200 API responses.
