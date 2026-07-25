/*
 * Settings ("/settings") — read-only current policy; separate preview/apply
 * flows for retention, exports, adapters, and backups (contracts/dashboard.yaml
 * panelId: settings-impact-preview); plus the §9 Appearance playground.
 *
 * This dashboard's backend (internal/webui/webui.go) deliberately never
 * embeds the mutation bearer: "the dashboard is read-only (all 14 routes are
 * GET analytics views)". Accordingly this page builds NO buttons or forms
 * that call mutation-gated endpoints (/api/v1/plans/preview,
 * /api/v1/plans/apply, /api/v1/admin/retention/*, /api/v1/admin/export,
 * /api/v1/admin/backup, etc.) — it only shows the current read-only snapshot
 * (system.database_size_bytes, system.backup_age_seconds,
 * system.restore_test_age_seconds from /api/v1/system/snapshot) plus a plain
 * note directing operators to the CLI/operator surface for preview/apply
 * actions.
 *
 * The Appearance panel (§9) is a pure client preference — theme, sidebar
 * collapse, and the two runtime-mutable accent tokens — persisted to
 * localStorage only, never sent to the backend.
 */
import { useState } from "react";
import { GapNote, Panel } from "../components/Panel";
import { StatusBadge } from "../components/StatusBadge";
import { Dropdown } from "../components/Dropdown";
import { deriveViewState } from "../api/client";
import { useSystemSnapshot } from "../api/queries";
import { bytesToReadable, secondsToReadable } from "../lib/format";
import { contrastRatio, PRESETS, THEME_BG, useTheme } from "../theme";
import "./Settings.css";

function AccentField({
  label,
  which,
}: {
  label: string;
  which: "purple" | "gold";
}) {
  const { appearance, setAccent } = useTheme();
  const current =
    which === "purple"
      ? appearance.theme === "light"
        ? appearance.accentPurpleLight
        : appearance.accentPurple
      : appearance.theme === "light"
        ? appearance.accentGoldLight
        : appearance.accentGold;
  const [draft, setDraft] = useState(current);
  const [rejected, setRejected] = useState(false);

  const ratio = contrastRatio(draft, THEME_BG[appearance.theme]);

  const commit = () => {
    const ok = setAccent(which, draft);
    setRejected(!ok);
    if (!ok) setDraft(current);
  };

  return (
    <div className="k-accent-field">
      <label className="t-caption" htmlFor={`accent-${which}`}>
        {label}
      </label>
      <div className="k-accent-field__row">
        <input
          id={`accent-${which}`}
          type="color"
          value={/^#[0-9a-fA-F]{6}$/.test(draft) ? draft : current}
          onChange={(e) => setDraft(e.target.value)}
          onBlur={commit}
        />
        <input
          className="k-accent-field__hex t-body"
          type="text"
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          onBlur={commit}
          aria-label={`${label} hex value`}
        />
        <span className="t-caption" style={{ color: "var(--text-faint)" }}>
          {ratio ? `${ratio.toFixed(2)}:1` : "invalid"}
        </span>
      </div>
      {rejected && (
        <p className="t-caption" style={{ color: "var(--status-degraded)" }}>
          Rejected: below the 4.5:1 AA contrast minimum against the current theme
          background. Reverted to the last accepted value.
        </p>
      )}
    </div>
  );
}

function AppearancePanel() {
  const { appearance, setTheme, toggleTheme, setSidebarCollapsed, applyPreset } = useTheme();

  return (
    <Panel title="Appearance" caption="Client-side preference only — never sent to the backend.">
      <div className="k-grid k-grid--2col">
        <div>
          <Dropdown
            caption="THEME"
            options={[
              { value: "dark", label: "Dark" },
              { value: "light", label: "Light" },
            ]}
            value={appearance.theme}
            onChange={(v) => setTheme(v === "light" ? "light" : "dark")}
          />
        </div>
        <div>
          <Dropdown
            caption="PRESET"
            options={PRESETS.map((p) => ({ value: p.id, label: p.label }))}
            value={appearance.preset}
            onChange={(v) => applyPreset(v as typeof appearance.preset)}
          />
        </div>
      </div>
      <div className="k-grid k-grid--2col">
        <AccentField label="Accent (purple)" which="purple" />
        <AccentField label="Accent (gold)" which="gold" />
      </div>
      <label className="k-checkbox-row t-body">
        <input
          type="checkbox"
          checked={appearance.sidebarCollapsed}
          onChange={(e) => setSidebarCollapsed(e.target.checked)}
        />
        Collapse sidebar by default
      </label>
      <button type="button" className="k-link-button t-caption" onClick={toggleTheme}>
        Toggle theme
      </button>
    </Panel>
  );
}

export function Settings() {
  const snapshot = useSystemSnapshot();
  const snap = snapshot.data?.data;
  const state = deriveViewState(snapshot.data, { isLoading: snapshot.isLoading });

  return (
    <section className="k-page">
      <header className="k-page__head">
        <h1 className="t-page-title">Settings</h1>
        <p className="k-page__wire t-caption">
          Read-only current policy; separate preview/apply flows for retention, exports,
          adapters, and backups.
        </p>
      </header>

      <AppearancePanel />

      <Panel title="Current state (read-only)">
        <dl className="k-kv">
          <div className="k-kv__row">
            <dt className="t-caption">Database size</dt>
            <dd className="t-body">
              {snap ? (
                bytesToReadable(snap.database_size_bytes)
              ) : (
                <StatusBadge state={state === "loading" ? "unknown" : state} glyphOnly />
              )}
            </dd>
          </div>
          <div className="k-kv__row">
            <dt className="t-caption">Backup age</dt>
            <dd className="t-body">
              {snap?.backup_age_seconds != null ? (
                secondsToReadable(snap.backup_age_seconds)
              ) : (
                <StatusBadge state="not_observed" glyphOnly />
              )}
            </dd>
          </div>
          <div className="t-caption k-kv__row">
            <dt>Backup checksum</dt>
            <dd className="t-body">
              {snap?.backup_checksum_ok === undefined ? (
                <StatusBadge state="not_observed" glyphOnly />
              ) : snap.backup_checksum_ok ? (
                "OK"
              ) : (
                "Failed"
              )}
            </dd>
          </div>
          <div className="k-kv__row">
            <dt className="t-caption">Restore test age</dt>
            <dd className="t-body">
              {snap?.restore_test_age_seconds != null ? (
                secondsToReadable(snap.restore_test_age_seconds)
              ) : (
                <StatusBadge state="not_observed" glyphOnly />
              )}
            </dd>
          </div>
          <div className="k-kv__row">
            <dt className="t-caption">Restore test result</dt>
            <dd className="t-body">
              {snap?.restore_test_passed === undefined ? (
                <StatusBadge state="not_observed" glyphOnly />
              ) : snap.restore_test_passed ? (
                "Passed"
              ) : (
                "Failed"
              )}
            </dd>
          </div>
        </dl>
        <GapNote>
          This dashboard never receives the mutation bearer (see{" "}
          <code>internal/webui/webui.go</code>): all 14 routes are read-only GET analytics
          views. Retention, export, adapter and backup preview/apply operations are
          performed via the CLI or operator surface, not from this page.
        </GapNote>
      </Panel>
    </section>
  );
}
