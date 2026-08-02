import assert from "node:assert/strict";
import test from "node:test";
import type { PluginProfileResponse, SkillProfileResponse } from "../src/api/types.ts";
import {
  normalizePluginProfile,
  normalizeSkillProfile,
} from "../src/api/normalize.ts";

test("legacy nullable skill collections normalize to empty arrays", () => {
  const profile = {
    assertions: null,
    sources: null,
    file_tree: null,
  } as unknown as SkillProfileResponse;

  const normalized = normalizeSkillProfile(profile);

  assert.deepEqual(normalized.assertions, []);
  assert.deepEqual(normalized.sources, []);
  assert.deepEqual(normalized.file_tree, []);
});

test("legacy nullable plugin collections normalize to empty arrays", () => {
  const profile = {
    children: null,
    versions: null,
    assertions: null,
    sources: null,
  } as unknown as PluginProfileResponse;

  const normalized = normalizePluginProfile(profile);

  assert.deepEqual(normalized.children, []);
  assert.deepEqual(normalized.versions, []);
  assert.deepEqual(normalized.assertions, []);
  assert.deepEqual(normalized.sources, []);
});
