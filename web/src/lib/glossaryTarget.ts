export const GLOSSARY_TARGET_PULSE_CLASS = "is-target-pulsing";
export const GLOSSARY_TARGET_PULSE_MS = 5_000;

export interface GlossaryPulseTarget {
  classList: {
    add(name: string): void;
    remove(name: string): void;
  };
  scrollIntoView(options?: ScrollIntoViewOptions): void;
  focus?(options?: FocusOptions): void;
}

export interface GlossaryPulseRuntime {
  findTarget(id: string): GlossaryPulseTarget | null;
  requestFrame(callback: () => void): number;
  cancelFrame(id: number): void;
  setTimer(callback: () => void, delay: number): number;
  clearTimer(id: number): void;
}

function targetID(hash: string): string | null {
  if (!hash.startsWith("#") || hash.length < 2) return null;
  try {
    return decodeURIComponent(hash.slice(1)) || null;
  } catch {
    return null;
  }
}

/**
 * Restarts one deterministic paint-only target cue and returns its cleanup.
 * The caller owns hashchange/listener lifecycle.
 */
export function startGlossaryTargetPulse(
  hash: string,
  runtime: GlossaryPulseRuntime,
): () => void {
  const id = targetID(hash);
  if (!id) return () => undefined;
  const target = runtime.findTarget(id);
  if (!target) return () => undefined;

  target.classList.remove(GLOSSARY_TARGET_PULSE_CLASS);
  target.scrollIntoView({ block: "center" });
  target.focus?.({ preventScroll: true });

  let timerID: number | null = null;
  const frameID = runtime.requestFrame(() => {
    target.classList.add(GLOSSARY_TARGET_PULSE_CLASS);
    timerID = runtime.setTimer(
      () => target.classList.remove(GLOSSARY_TARGET_PULSE_CLASS),
      GLOSSARY_TARGET_PULSE_MS,
    );
  });

  return () => {
    runtime.cancelFrame(frameID);
    if (timerID !== null) runtime.clearTimer(timerID);
    target.classList.remove(GLOSSARY_TARGET_PULSE_CLASS);
  };
}
