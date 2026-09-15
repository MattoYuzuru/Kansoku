// Build-time generator: reads the authoritative dashboard and glossary
// registries (JSON-encoded despite the .yaml suffix) and emits typed frontend
// modules, so routes and user-facing definitions never drift from contracts.
// Run automatically by `prebuild` and `dev`.
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const repoRoot = path.resolve(here, "..", "..");
const contractPath = path.join(repoRoot, "contracts", "dashboard.yaml");
const glossaryPath = path.join(repoRoot, "contracts", "glossary.yaml");
const outDir = path.join(here, "..", "src", "generated");
const outFile = path.join(outDir, "routes.ts");
const glossaryOutFile = path.join(outDir, "glossary.ts");

const raw = fs.readFileSync(contractPath, "utf8");
let contract;
try {
  contract = JSON.parse(raw);
} catch (err) {
  throw new Error(
    `contracts/dashboard.yaml is expected to be JSON-encoded; parse failed: ${err.message}`,
  );
}

if (!Array.isArray(contract.routes) || contract.routes.length !== 17) {
  throw new Error(
    `expected exactly 17 routes in the contract, found ${contract.routes?.length}`,
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

const glossaryRaw = fs.readFileSync(glossaryPath, "utf8");
let glossary;
try {
  glossary = JSON.parse(glossaryRaw);
} catch (err) {
  throw new Error(
    `contracts/glossary.yaml is expected to be JSON-encoded; parse failed: ${err.message}`,
  );
}
if (!Array.isArray(glossary.terms) || glossary.terms.length === 0) {
  throw new Error("contracts/glossary.yaml must contain at least one term");
}
const glossaryTerms = glossary.terms.map((term) => ({
  id: term.id,
  definition: term.definition,
  plainDefinition: term.plain_definition ?? term.definition,
}));
const glossaryBanner = `// AUTO-GENERATED from contracts/glossary.yaml by web/scripts/gen-routes.mjs.
// Do not edit by hand. Regenerate: \`npm run gen:routes\` (runs on prebuild).
// contract_version: ${glossary.contract_version}, schema_version: ${glossary.schema_version}\n`;
const glossaryBody = `export interface GlossaryTerm {
  readonly id: string;
  readonly definition: string;
  readonly plainDefinition: string;
}

export const GLOSSARY_TERMS: readonly GlossaryTerm[] = ${JSON.stringify(glossaryTerms, null, 2)} as const;

export const GLOSSARY_BY_ID = new Map(GLOSSARY_TERMS.map((term) => [term.id, term]));
`;
fs.writeFileSync(glossaryOutFile, glossaryBanner + "\n" + glossaryBody);

console.log(
  `gen-routes: wrote ${routes.length} routes and ${glossaryTerms.length} terms -> ${path.relative(repoRoot, outDir)}`,
);
