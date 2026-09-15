import assert from "node:assert/strict";
import test from "node:test";
import {
  RANGE_PREFERENCE_STORAGE_KEY,
  loadRangePreferences,
  readRangePreference,
  writeRangePreference,
  type RangePreferenceStorage,
} from "../src/lib/rangePreference.ts";

class MemoryStorage implements RangePreferenceStorage {
  readonly values = new Map<string, string>();
  throwOnRead = false;
  throwOnWrite = false;

  getItem(key: string): string | null {
    if (this.throwOnRead) throw new Error("storage_disabled");
    return this.values.get(key) ?? null;
  }

  setItem(key: string, value: string): void {
    if (this.throwOnWrite) throw new Error("storage_disabled");
    this.values.set(key, value);
  }
}

test("range preferences retain presets independently under stable page keys", () => {
  const storage = new MemoryStorage();
  assert.equal(writeRangePreference(storage, "activity", "week"), true);
  assert.equal(writeRangePreference(storage, "models", "year"), true);

  assert.equal(readRangePreference(storage, "activity", "month"), "week");
  assert.equal(readRangePreference(storage, "models", "month"), "year");
  assert.deepEqual(loadRangePreferences(storage), {
    version: 1,
    pages: { activity: "week", models: "year" },
  });
});

test("range preferences ignore corrupt, old-version, unknown-page, and invalid-preset data", () => {
  const storage = new MemoryStorage();
  for (const raw of [
    "{",
    JSON.stringify({ version: 0, pages: { activity: "week" } }),
    JSON.stringify({ version: 1, pages: { activity: "forever" } }),
    JSON.stringify({ version: 1, pages: { activity: "week", invented: "year" } }),
  ]) {
    storage.values.set(RANGE_PREFERENCE_STORAGE_KEY, raw);
    assert.equal(readRangePreference(storage, "activity", "month"), "month");
  }
});

test("disabled storage falls back without throwing and remains writable in memory", () => {
  const storage = new MemoryStorage();
  storage.throwOnRead = true;
  assert.equal(readRangePreference(storage, "activity", "day"), "day");

  storage.throwOnRead = false;
  storage.throwOnWrite = true;
  assert.equal(writeRangePreference(storage, "activity", "week"), false);
  assert.equal(readRangePreference(storage, "activity", "day"), "day");
});

test("a write merges the latest document instead of overwriting another tab's page", () => {
  const storage = new MemoryStorage();
  storage.values.set(
    RANGE_PREFERENCE_STORAGE_KEY,
    JSON.stringify({ version: 1, pages: { activity: "week" } }),
  );

  assert.equal(writeRangePreference(storage, "models", "year"), true);
  assert.equal(readRangePreference(storage, "activity", "month"), "week");
  assert.equal(readRangePreference(storage, "models", "month"), "year");
});
