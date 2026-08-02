import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import {
  modelDrilldownHref,
  modelFilterFromSearch,
  modelDrilldownState,
  selectedModelRow,
} from "../src/lib/modelDrilldown.ts";

const rows = [
  { entity_id: "openai/gpt-5", requests: 12 },
  { entity_id: "claude?variant=sonnet&mode=fast", requests: 7 },
] as const;

test("model drill-down links preserve reserved model ID characters", () => {
  const modelID = rows[1].entity_id;
  const href = modelDrilldownHref(modelID);

  assert.equal(href, "/models?model=claude%3Fvariant%3Dsonnet%26mode%3Dfast");
  assert.equal(modelFilterFromSearch(href.slice(href.indexOf("?"))), modelID);
});

test("the canonical /models route remains an unfiltered global comparison", () => {
  assert.equal(modelFilterFromSearch(""), null);
  assert.equal(selectedModelRow("", rows), null);
  assert.deepEqual(rows.map((row) => row.entity_id), [
    "openai/gpt-5",
    "claude?variant=sonnet&mode=fast",
  ]);
});

test("a direct URL selects only an exact row without mutating global rows", () => {
  const selected = selectedModelRow("?model=openai%2Fgpt-5", rows);

  assert.deepEqual(selected, rows[0]);
  assert.equal(selectedModelRow("?model=missing", rows), null);
  assert.equal(rows.length, 2);
});

test("empty filters are rejected while durable TEXT model IDs round-trip exactly", () => {
  const longModelID = `${"m".repeat(257)}\nvariant`;
  const href = modelDrilldownHref(longModelID);

  assert.equal(modelFilterFromSearch("?model="), null);
  assert.equal(modelFilterFromSearch(href.slice(href.indexOf("?"))), longModelID);
});

test("query failures remain errors instead of becoming not_observed", () => {
  assert.equal(modelDrilldownState({
    requested: true,
    modelID: "openai/gpt-5",
    selected: false,
    isLoading: false,
    isError: true,
  }), "error");
  assert.equal(modelDrilldownState({
    requested: true,
    modelID: "openai/gpt-5",
    selected: false,
    isLoading: false,
    isError: false,
  }), "not_observed");
  assert.equal(modelDrilldownState({
    requested: true,
    modelID: "openai/gpt-5",
    selected: true,
    isLoading: false,
    isError: true,
  }), "selected");
});

test("the dashboard contract retains canonical Fleet, Installation, and Models routes", () => {
  const contract = JSON.parse(
    readFileSync(new URL("../../contracts/dashboard.yaml", import.meta.url), "utf8"),
  ) as {
    routes: Array<{ path: string; title: string; wireframe: string }>;
  };
  const byPath = new Map(contract.routes.map((route) => [route.path, route]));

  assert.equal(byPath.get("/agents")?.title, "Fleet");
  assert.equal(byPath.get("/agents/:id")?.title, "Installation detail");
  assert.match(byPath.get("/models")?.wireframe ?? "", /global cross-installation/);
  assert.match(byPath.get("/models")?.wireframe ?? "", /URL-addressable exact-model/);
  assert.equal(contract.routes.filter((route) => route.path === "/models").length, 1);
});
