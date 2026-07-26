/*
 * AppShell — the persistent left sidebar (§2) + route outlet.
 *
 * Sidebar: 248px expanded / 60px collapsed, --surface background, single 1px
 * --border right hairline. Brand block (28x28 placeholder chip with mono "K",
 * "Kansoku" wordmark, gold "LOCAL" tag). 7 nav groups from nav.ts. Active route
 * shows a 3px --active-marker left bar + --row-selected fill + 600 weight.
 * Auto-collapses at <=1024px; manual toggle persists to localStorage via the
 * theme provider's `sidebarCollapsed`.
 *
 * Keyboard focus uses per-element :focus-visible outlines (no shared node —
 * see .k-nav__row:focus-visible / .k-iconbtn:focus-visible in AppShell.css).
 */
import { useEffect, useMemo, useState } from "react";
import type { ReactNode } from "react";
import { Link, useLocation } from "wouter";
import { Icon } from "./ui/icons";
import { NAV_GROUPS, SETTINGS_ITEM, activeNavPath, type NavItem } from "./nav";
import { useTheme } from "./theme";
import "./AppShell.css";

const BREAKPOINT = 1024;

export function AppShell({ children }: { children: ReactNode }) {
  const { appearance, setSidebarCollapsed, toggleTheme } = useTheme();
  const [location] = useLocation();
  const active = useMemo(() => activeNavPath(location), [location]);

  // Auto-collapse at <=1024px, but let the manual preference win above it.
  const [narrow, setNarrow] = useState(
    () => typeof window !== "undefined" && window.innerWidth <= BREAKPOINT,
  );
  useEffect(() => {
    const onResize = () => setNarrow(window.innerWidth <= BREAKPOINT);
    window.addEventListener("resize", onResize);
    return () => window.removeEventListener("resize", onResize);
  }, []);
  const collapsed = narrow || appearance.sidebarCollapsed;

  const renderItem = (item: NavItem) => {
    const isActive = active === item.path;
    return (
      <li key={item.path}>
        <Link
          href={item.path}
          className={`k-nav__row${isActive ? " is-active" : ""}`}
          aria-current={isActive ? "page" : undefined}
          title={collapsed ? item.label : undefined}
        >
          <span className="k-nav__icon" aria-hidden="true">
            <Icon name={item.icon} size={20} />
          </span>
          <span className="k-nav__label">{item.label}</span>
        </Link>
      </li>
    );
  };

  return (
    <div className={`k-shell${collapsed ? " is-collapsed" : ""}`}>
      <aside className="k-sidebar" aria-label="Primary">
        {/* Brand block */}
        <div className="k-brand">
          <span className="k-brand__chip" aria-hidden="true">
            K
          </span>
          <span className="k-brand__text">
            <span className="k-brand__wordmark">Kansoku</span>
            <span className="k-brand__tag">LOCAL</span>
          </span>
        </div>

        <nav className="k-nav">
          {NAV_GROUPS.map((group, gi) => (
            <div
              className={`k-nav__group${group.compactWithPrevious ? " k-nav__group--compact" : ""}`}
              key={group.label ?? `g${gi}`}
            >
              {group.label && <div className="k-nav__group-label t-section-header">{group.label}</div>}
              <ul className="k-nav__list">{group.items.map(renderItem)}</ul>
            </div>
          ))}

          {/* Settings pinned to the bottom behind a full-width hairline (§2) */}
          <div className="k-nav__pinned">
            <ul className="k-nav__list">{renderItem(SETTINGS_ITEM)}</ul>
          </div>
        </nav>

        {/* Rail footer: collapse toggle + theme toggle */}
        <div className="k-sidebar__foot">
          <button
            type="button"
            className="k-iconbtn"
            aria-label={appearance.sidebarCollapsed ? "Expand sidebar" : "Collapse sidebar"}
            aria-pressed={collapsed}
            disabled={narrow}
            onClick={() => setSidebarCollapsed(!appearance.sidebarCollapsed)}
          >
            <Icon
              name={collapsed ? "layout-sidebar-left-expand" : "layout-sidebar-left-collapse"}
              size={20}
            />
          </button>
          <button
            type="button"
            className="k-iconbtn k-iconbtn--theme"
            aria-label={appearance.theme === "dark" ? "Switch to light theme" : "Switch to dark theme"}
            onClick={toggleTheme}
          >
            <Icon name={appearance.theme === "dark" ? "sun" : "moon"} size={20} />
          </button>
        </div>
      </aside>

      <main className="k-content">
        <div className="k-content__inner">{children}</div>
      </main>
    </div>
  );
}
