/*
 * wouter route table for the contracts/dashboard.yaml paths. Each
 * route renders its real page component, wired to the live /api/v1 surface.
 * Per-route document.title = `Kansoku · {route.title}` (or
 * `Kansoku · Agent {alias}` for /agents/:id, opaque alias only), sourced from
 * the generated route registry so it can never drift from the contract.
 *
 * Every page is a lazy chunk: most pages pull in the ~370 KiB gzip echarts
 * vendor chunk via ChartContainer, and an eager import would put that on
 * every page's initial load even for chart-free routes (Agents, Settings,
 * System). React.lazy defers each page's (and its share of the echarts
 * chunk's) download to the moment its route is actually visited.
 */
import { Suspense, lazy, useEffect } from "react";
import { Route, Switch, useParams } from "wouter";
import { ROUTES, type RouteMeta } from "./generated/routes";

const Overview = lazy(() => import("./pages/Overview").then((m) => ({ default: m.Overview })));
const Activity = lazy(() => import("./pages/Activity").then((m) => ({ default: m.Activity })));
const Prompts = lazy(() => import("./pages/Prompts").then((m) => ({ default: m.Prompts })));
const Agents = lazy(() => import("./pages/Agents").then((m) => ({ default: m.Agents })));
const AgentDetail = lazy(() => import("./pages/AgentDetail").then((m) => ({ default: m.AgentDetail })));
const Models = lazy(() => import("./pages/Models").then((m) => ({ default: m.Models })));
const Skills = lazy(() => import("./pages/Skills").then((m) => ({ default: m.Skills })));
const SkillDetail = lazy(() => import("./pages/SkillDetail").then((m) => ({ default: m.SkillDetail })));
const Plugins = lazy(() => import("./pages/Plugins").then((m) => ({ default: m.Plugins })));
const PluginDetail = lazy(() => import("./pages/PluginDetail").then((m) => ({ default: m.PluginDetail })));
const MCP = lazy(() => import("./pages/MCP").then((m) => ({ default: m.MCP })));
const MCPServerDetail = lazy(() => import("./pages/MCPServerDetail").then((m) => ({ default: m.MCPServerDetail })));
const MCPToolDetail = lazy(() => import("./pages/MCPToolDetail").then((m) => ({ default: m.MCPToolDetail })));
const Tools = lazy(() => import("./pages/Tools").then((m) => ({ default: m.Tools })));
const Reliability = lazy(() => import("./pages/Reliability").then((m) => ({ default: m.Reliability })));
const Privacy = lazy(() => import("./pages/Privacy").then((m) => ({ default: m.Privacy })));
const System = lazy(() => import("./pages/System").then((m) => ({ default: m.System })));
const Glossary = lazy(() => import("./pages/Glossary").then((m) => ({ default: m.Glossary })));
const Settings = lazy(() => import("./pages/Settings").then((m) => ({ default: m.Settings })));

const TITLE_PREFIX = "Kansoku";

function setTitle(title: string) {
  document.title = `${TITLE_PREFIX} · ${title}`;
}

function byPath(path: string): RouteMeta {
  const route = ROUTES.find((r) => r.path === path);
  if (!route) throw new Error(`route not found in contract: ${path}`);
  return route;
}

// Maps each static contract path to its real page component. Titles are
// still sourced from the generated registry (byPath) so they can never drift
// from contracts/dashboard.yaml.
const PAGE_BY_PATH: Record<string, React.ComponentType> = {
  "/": Overview,
  "/activity": Activity,
  "/prompts": Prompts,
  "/agents": Agents,
  "/models": Models,
  "/components/skills": Skills,
  "/components/plugins": Plugins,
  "/components/mcp": MCP,
  "/tools": Tools,
  "/reliability": Reliability,
  "/privacy": Privacy,
  "/system": System,
  "/glossary": Glossary,
  "/settings": Settings,
};

function PageRoute({ path }: { path: string }) {
  const route = byPath(path);
  useEffect(() => setTitle(route.title), [route.title]);
  const Page = PAGE_BY_PATH[path];
  return <Page />;
}

function AgentDetailRoute() {
  const params = useParams();
  const alias = params.id ?? "";
  useEffect(() => setTitle(`Agent ${alias}`), [alias]);
  return <AgentDetail alias={alias} />;
}

function SkillDetailRoute() {
  const params = useParams();
  const id = params.id ?? "";
  useEffect(() => setTitle(`Skill ${id}`), [id]);
  return <SkillDetail id={id} />;
}

function PluginDetailRoute() {
  const params = useParams();
  const id = params.id ?? "";
  useEffect(() => setTitle(`Plugin ${id}`), [id]);
  return <PluginDetail id={id} />;
}

function MCPServerDetailRoute() {
  const params = useParams();
  const id = params.id ?? "";
  useEffect(() => setTitle(`MCP ${id}`), [id]);
  return <MCPServerDetail id={id} />;
}

function MCPToolDetailRoute() {
  const params = useParams();
  const serverID = params.id ?? "";
  const toolID = params.toolID ?? "";
  useEffect(() => setTitle(`MCP tool ${toolID}`), [toolID]);
  return <MCPToolDetail serverID={serverID} toolID={toolID} />;
}

function NotFound() {
  useEffect(() => setTitle("Not found"), []);
  return (
    <section className="k-page">
      <h1 className="t-page-title">Not found</h1>
      <p className="t-body" style={{ color: "var(--text-muted)" }}>
        No dashboard route matches this path.
      </p>
    </section>
  );
}

// Static-path routes (excludes the dynamic /agents/:id, handled explicitly).
const STATIC_PATHS = ROUTES.map((r) => r.path).filter((p) => !p.includes(":"));

export function AppRoutes() {
  return (
    <Suspense fallback={<section className="k-page" aria-busy="true" />}>
      <Switch>
        {STATIC_PATHS.map((path) => (
          <Route key={path} path={path}>
            <PageRoute path={path} />
          </Route>
        ))}
        <Route path="/agents/:id">
          <AgentDetailRoute />
        </Route>
        <Route path="/components/skills/:id">
          <SkillDetailRoute />
        </Route>
        <Route path="/components/plugins/:id">
          <PluginDetailRoute />
        </Route>
        <Route path="/components/mcp/:id/tools/:toolID">
          <MCPToolDetailRoute />
        </Route>
        <Route path="/components/mcp/:id">
          <MCPServerDetailRoute />
        </Route>
        <Route>
          <NotFound />
        </Route>
      </Switch>
    </Suspense>
  );
}
