import assert from "node:assert/strict";
import test from "node:test";
import {
  GLOSSARY_TARGET_PULSE_CLASS,
  GLOSSARY_TARGET_PULSE_MS,
  startGlossaryTargetPulse,
  type GlossaryPulseRuntime,
} from "../src/lib/glossaryTarget.ts";

test("glossary target pulse is deterministic, focusable, and self-clearing", () => {
  const classes = new Set<string>();
  let scrolled = false;
  let focused = false;
  let cleanup: (() => void) | undefined;
  let cleanupDelay = 0;
  const runtime: GlossaryPulseRuntime = {
    findTarget: (id) => id === "invoked" ? {
      classList: {
        add: (name) => classes.add(name),
        remove: (name) => classes.delete(name),
      },
      scrollIntoView: () => { scrolled = true; },
      focus: () => { focused = true; },
    } : null,
    requestFrame: (callback) => { callback(); return 1; },
    cancelFrame: () => undefined,
    setTimer: (callback, delay) => {
      cleanup = callback;
      cleanupDelay = delay;
      return 2;
    },
    clearTimer: () => undefined,
  };

  const stop = startGlossaryTargetPulse("#invoked", runtime);
  assert.equal(scrolled, true);
  assert.equal(focused, true);
  assert.equal(classes.has(GLOSSARY_TARGET_PULSE_CLASS), true);
  assert.equal(cleanupDelay, GLOSSARY_TARGET_PULSE_MS);
  cleanup?.();
  assert.equal(classes.has(GLOSSARY_TARGET_PULSE_CLASS), false);
  stop();
});

test("invalid or empty hashes are ignored without scheduling animation", () => {
  let scheduled = false;
  const runtime: GlossaryPulseRuntime = {
    findTarget: () => null,
    requestFrame: () => { scheduled = true; return 1; },
    cancelFrame: () => undefined,
    setTimer: () => 1,
    clearTimer: () => undefined,
  };

  for (const hash of ["", "#", "#%E0%A4%A"]) {
    startGlossaryTargetPulse(hash, runtime)();
  }
  assert.equal(scheduled, false);
});
