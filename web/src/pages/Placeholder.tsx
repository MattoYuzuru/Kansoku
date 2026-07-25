/*
 * Placeholder page — every one of the 14 routes points here for the scaffold.
 * Renders the route's page title (from the generated contract registry) and a
 * "route scaffolded, content pending" note plus the panel ids the real page
 * (a later task) will fill in. This is intentionally content-free.
 */
import type { RouteMeta } from "../generated/routes";
import "./Placeholder.css";

export interface PlaceholderProps {
  route: RouteMeta;
  /** For /agents/:id, the opaque alias to render (safe_url_policy). */
  agentAlias?: string;
}

export function Placeholder({ route, agentAlias }: PlaceholderProps) {
  const heading = agentAlias ? `Agent ${agentAlias}` : route.title;
  return (
    <section className="k-page">
      <header className="k-page__head">
        <h1 className="t-page-title">{heading}</h1>
        <p className="k-page__wire t-caption">{route.wireframe}</p>
      </header>
      <div className="k-page__pending">
        <div className="t-section-header k-page__pending-label">Scaffolded</div>
        <p className="t-body">
          Route scaffolded, content pending. This shell wires routing, titles,
          theme and the design-token layer; per-panel charts and tables are a
          separate follow-up task.
        </p>
        {route.panelIds.length > 0 && (
          <ul className="k-page__panels">
            {route.panelIds.map((id) => (
              <li key={id} className="t-table-cell">
                {id}
              </li>
            ))}
          </ul>
        )}
      </div>
    </section>
  );
}
