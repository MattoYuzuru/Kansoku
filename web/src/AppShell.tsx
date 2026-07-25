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
 * Keyboard focus uses a single shared travelling focus-ring DOM node that
 * animates transform between focused nav rows (§2 / §4 #6).
 */
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { FocusEvent as ReactFocusEvent, ReactNode } from "react";
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

  // Shared travelling focus-ring node.
  const navRef = useRef<HTMLElement>(null);
  const [ring, setRing] = useState<{ top: number; height: number; visible: boolean }>({
    top: 0,
    height: 0,
    visible: false,
  });
  const onRowFocus = useCallback((e: ReactFocusEvent) => {
    const nav = navRef.current;
    if (!nav) return;
    const target = e.currentTarget as HTMLElement;
    const r = target.getBoundingClientRect();
    const base = nav.getBoundingClientRect();
    setRing({ top: r.top - base.top, height: r.height, visible: true });
  }, []);
  const onRowBlur = useCallback(() => setRing((s) => ({ ...s, visible: false })), []);

  const renderItem = (item: NavItem) => {
    const isActive = active === item.path;
    return (
      <li key={item.path}>
        <Link
          href={item.path}
          className={`k-nav__row${isActive ? " is-active" : ""}`}
          aria-current={isActive ? "page" : undefined}
          onFocus={onRowFocus}
          onBlur={onRowBlur}
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

        <nav className="k-nav" ref={navRef}>
          {/* Shared travelling focus-ring (§4 #6) */}
          <span
            className={`k-nav__focus-ring${ring.visible ? " is-visible" : ""}`}
            style={{ transform: `translateY(${ring.top}px)`, height: `${ring.height}px` }}
            aria-hidden="true"
          />
          {NAV_GROUPS.map((group, gi) => (
            <div className="k-nav__group" key={group.label ?? `g${gi}`}>
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
            onFocus={onRowFocus}
            onBlur={onRowBlur}
          >
            <Icon
              name={collapsed ? "layout-sidebar-left-expand" : "layout-sidebar-left-collapse"}
              size={20}
            />
          </button>
          <button
            type="button"
            className="k-iconbtn"
            aria-label={appearance.theme === "dark" ? "Switch to light theme" : "Switch to dark theme"}
            onClick={toggleTheme}
            onFocus={onRowFocus}
            onBlur={onRowBlur}
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
