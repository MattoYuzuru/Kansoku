import assert from "node:assert/strict";
import test from "node:test";
import {
  formatMetric,
  formatMetricWithRaw,
} from "../src/lib/format.ts";

test("shared metric formatter keeps at most two fractional digits", () => {
  assert.equal(formatMetric(12), "12");
  assert.match(formatMetric(12.3456), /^12[.,]35$/);
  assert.match(formatMetric(4_008_574.097), /^4(?:[,\u00a0\u202f ]?)008(?:[,\u00a0\u202f ]?)574[.,]1$/);
  assert.doesNotMatch(formatMetric(1.23456), /[.,]\d{3}/);
});

test("tooltip formatter preserves the unrounded raw value", () => {
  const formatted = formatMetricWithRaw(12.3456, "ms");
  assert.match(formatted, /^12[.,]35 ms · raw 12\.3456 ms$/);
  assert.equal(formatMetricWithRaw(null, "ms"), "—");
});
