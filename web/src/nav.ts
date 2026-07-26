/*
 * Sidebar navigation model — the §2 table mapping the 14 contracts/dashboard.yaml
 * routes into 7 nav entries with Tabler icons. Titles come from the generated
 * route registry so they never drift from the contract.
 */
import { ROUTES } from "./generated/routes";
import type { IconName } from "./ui/icons";

function title(path: string): string {
  return ROUTES.find((r) => r.path === path)?.title ?? path;
}

export interface NavItem {
  path: string;
  label: string;
  icon: IconName;
}

export interface NavGroup {
  /** Mono uppercase group label, or null for an ungrouped single entry. */
  label: string | null;
  items: NavItem[];
  /** Keep a semantic group but render it adjacent to the preceding rows. */
  compactWithPrevious?: boolean;
}

// §2 grouping table. Order 1..7. Settings (order 7) is pinned to the bottom by
// the shell layout, not by this data.
export const NAV_GROUPS: readonly NavGroup[] = [
  { label: null, items: [{ path: "/", label: title("/"), icon: "layout-dashboard" }] },
  {
    label: "ACTIVITY",
    items: [
      { path: "/activity", label: title("/activity"), icon: "timeline" },
      { path: "/prompts", label: title("/prompts"), icon: "message-2" },
    ],
  },
  {
    label: "FLEET",
    items: [
      { path: "/agents", label: title("/agents"), icon: "robot" },
      { path: "/models", label: title("/models"), icon: "cpu" },
    ],
  },
  {
    label: "COMPONENTS",
    items: [
      { path: "/components/skills", label: title("/components/skills"), icon: "sparkles" },
      { path: "/components/plugins", label: title("/components/plugins"), icon: "puzzle" },
      { path: "/components/mcp", label: title("/components/mcp"), icon: "plug-connected" },
      { path: "/tools", label: title("/tools"), icon: "tool" },
    ],
  },
  {
    label: null,
    compactWithPrevious: true,
    items: [{ path: "/reliability", label: title("/reliability"), icon: "heartbeat" }],
  },
  {
    label: "OPERATIONS",
    items: [
      { path: "/privacy", label: title("/privacy"), icon: "shield-lock" },
      { path: "/system", label: title("/system"), icon: "server-2" },
    ],
  },
];

// Settings is pinned to the bottom of the rail (§2 order 7).
export const SETTINGS_ITEM: NavItem = {
  path: "/settings",
  label: title("/settings"),
  icon: "settings",
};

/** The active nav path for a given location (longest matching prefix; /agents
 * stays active on /agents/:id per §2). */
export function activeNavPath(location: string): string {
  if (location === "/") return "/";
  const all = [...NAV_GROUPS.flatMap((g) => g.items), SETTINGS_ITEM];
  let best = "";
  for (const item of all) {
    if (item.path === "/") continue;
    if (location === item.path || location.startsWith(item.path + "/")) {
      if (item.path.length > best.length) best = item.path;
    }
  }
  return best;
}
