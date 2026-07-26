package codexadapter

import (
	"testing"
	"time"

	"kansoku.local/kansoku/internal/adaptersdk"
)

func TestBuildInventorySnapshotPreservesCodexPluginBundleGraph(t *testing.T) {
	snapshot, err := BuildInventorySnapshot(InventoryInput{
		InstallationID: "codex-plugin-fixture",
		Plugins: []PluginDescriptor{{
			Name: "kansoku-session16-canary", Version: "1.0.0",
			Scope: adaptersdk.ScopeMarketplace, ActiveEnabledFor: "codex-plugin-fixture",
			PathPseudonym: "hmac-sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			Fingerprint:   stableHex("plugin-fixture"),
			BundledSkills: []SkillDescriptor{{
				Name: "kansoku-noop-skill", Scope: adaptersdk.ScopeMarketplace,
				PathPseudonym: "hmac-sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
				Fingerprint:   stableHex("plugin-skill"),
			}},
			BundledMCPServers: []MCPServerDescriptor{{
				Name: "kansoku-do-nothing", Scope: adaptersdk.ScopeMarketplace,
				AdvertisedTools: []string{"noop"},
				PathPseudonym:   "hmac-sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
				Fingerprint:     stableHex("plugin-mcp"),
			}},
			BundledApps: []AppDescriptor{{
				Name: "kansoku-canary-app", Scope: adaptersdk.ScopeMarketplace,
				PathPseudonym: "hmac-sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
				Fingerprint:   stableHex("plugin-app"),
			}},
		}},
	}, time.Date(2026, 7, 26, 16, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[adaptersdk.NodeKind]int{}
	for _, node := range snapshot.Nodes {
		kinds[node.Kind]++
	}
	if kinds[adaptersdk.NodePluginPackage] != 1 ||
		kinds[adaptersdk.NodeSkillIdentity] != 1 ||
		kinds[adaptersdk.NodeMCPServerInstance] != 1 ||
		kinds[adaptersdk.NodeMCPTool] != 1 ||
		kinds[adaptersdk.NodeAppDefinition] != 1 {
		t.Fatalf("bundle node kinds mismatch: %+v", kinds)
	}
	var bundles, provides, enabled int
	for _, edge := range snapshot.Edges {
		switch edge.Kind {
		case adaptersdk.EdgeBundles:
			bundles++
		case adaptersdk.EdgeProvides:
			provides++
		case adaptersdk.EdgeEnabledFor:
			enabled++
		}
	}
	if bundles != 2 || provides != 2 || enabled != 1 {
		t.Fatalf("bundle edges mismatch: bundles=%d provides=%d enabled=%d", bundles, provides, enabled)
	}
}
