export const timeline = Array.from({ length: 180 }, (_, index) => ({
  minute: index,
  events: 30 + Math.round(12 * Math.sin(index / 11)) + (index % 17),
  p95Bytes: 900 + Math.round(450 * Math.cos(index / 19)) + index * 3,
}));

export const funnel = [
  ["installed", 500],
  ["enabled", 430],
  ["exposed", 310],
  ["invoked", 180],
  ["loaded", 155],
  ["executed", 130],
  ["succeeded", 118],
];
