/*
 * Small formatting/aggregation helpers reused across pages — day-label
 * formatting, byte/duration/currency formatting, and sum/last helpers over
 * daily series. No business logic beyond presentation; every number still
 * comes straight from the API response.
 */

export function dayLabel(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleDateString(undefined, { month: "short", day: "numeric" });
}

export function hourLabel(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleString(undefined, { month: "short", day: "numeric", hour: "2-digit" });
}

/** Shared presentation policy for measured values, including percentiles. */
export function formatMetric(value: number): string {
  return new Intl.NumberFormat(undefined, {
    maximumFractionDigits: 2,
  }).format(value);
}

/**
 * Tooltip/export-adjacent label: compact presentation plus the exact API
 * number whenever rounding would otherwise hide source precision.
 */
export function formatMetricWithRaw(
  value: number | null | undefined,
  unit?: string,
): string {
  if (value === null || value === undefined) return "—";
  const suffix = unit ? ` ${unit}` : "";
  const display = `${formatMetric(value)}${suffix}`;
  return Number.isInteger(value) ? display : `${display} · raw ${String(value)}${suffix}`;
}

export function sum(values: readonly (number | null | undefined)[]): number {
  return values.reduce<number>((acc, v) => acc + (v ?? 0), 0);
}

export function bytesToReadable(bytes: number | null | undefined): string {
  if (bytes === null || bytes === undefined) return "—";
  const units = ["B", "KiB", "MiB", "GiB", "TiB"];
  let value = bytes;
  let unitIndex = 0;
  while (value >= 1024 && unitIndex < units.length - 1) {
    value /= 1024;
    unitIndex += 1;
  }
  return `${formatMetric(value)} ${units[unitIndex]}`;
}

export function secondsToReadable(seconds: number | null | undefined): string {
  if (seconds === null || seconds === undefined) return "—";
  if (seconds < 60) return `${seconds.toFixed(0)}s`;
  const minutes = seconds / 60;
  if (minutes < 60) return `${minutes.toFixed(1)}m`;
  const hours = minutes / 60;
  if (hours < 48) return `${hours.toFixed(1)}h`;
  const days = hours / 24;
  return `${days.toFixed(1)}d`;
}

export function microsToUsd(micros: number): number {
  return micros / 1_000_000;
}

export function ratio(numerator: number, denominator: number): number | null {
  if (denominator <= 0) return null;
  return numerator / denominator;
}
