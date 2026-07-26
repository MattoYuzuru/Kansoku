// Build-time generator: reads the authoritative route registry from
// contracts/dashboard.yaml (which is JSON-encoded despite the .yaml suffix)
// and emits src/generated/routes.ts, so the frontend never hand-duplicates
// the path/title list. Run automatically by `prebuild` and `dev`.
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const repoRoot = path.resolve(here, "..", "..");
const contractPath = path.join(repoRoot, "contracts", "dashboard.yaml");
const outDir = path.join(here, "..", "src", "generated");
const outFile = path.join(outDir, "routes.ts");

const raw = fs.readFileSync(contractPath, "utf8");
let contract;
try {
  contract = JSON.parse(raw);
} catch (err) {
  throw new Error(
    `contracts/dashboard.yaml is expected to be JSON-encoded; parse failed: ${err.message}`,
  );
}

if (!Array.isArray(contract.routes) || contract.routes.length !== 15) {
  throw new Error(
    `expected exactly 15 routes in the contract, found ${contract.routes?.length}`,
  );
}

const routes = contract.routes.map((r) => ({
  path: r.path,
  title: r.title,
  wireframe: r.wireframe,
  panelIds: (r.panels ?? []).map((p) => p.id),
}));

fs.mkdirSync(outDir, { recursive: true });

const banner = `// AUTO-GENERATED from contracts/dashboard.yaml by web/scripts/gen-routes.mjs.
// Do not edit by hand. Regenerate: \`npm run gen:routes\` (runs on prebuild).
// contract_version: ${contract.contract_version}, schema_version: ${contract.schema_version}\n`;

const body = `export interface RouteMeta {
  readonly path: string;
  readonly title: string;
  readonly wireframe: string;
  readonly panelIds: readonly string[];
}

export const ROUTES: readonly RouteMeta[] = ${JSON.stringify(routes, null, 2)} as const;

export const GLOBAL_QUERY = ${JSON.stringify(contract.global_query, null, 2)} as const;
`;

fs.writeFileSync(outFile, banner + "\n" + body);
console.log(`gen-routes: wrote ${routes.length} routes -> ${path.relative(repoRoot, outFile)}`);
