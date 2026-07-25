/*
 * RangeControl — the one shared range selector every range-taking panel uses
 * (task scope boundary: a single control, not a per-page reinvention). Thin
 * wrapper around the existing Dropdown component bound to useRange's state.
 */
import { Dropdown } from "./Dropdown";
import type { RangeKey, UseRangeResult } from "../hooks/useRange";

export interface RangeControlProps {
  range: UseRangeResult;
  className?: string;
}

export function RangeControl({ range, className }: RangeControlProps) {
  return (
    <Dropdown
      caption="RANGE"
      className={className}
      options={range.options.map((o) => ({ value: o.value, label: o.label }))}
      value={range.rangeKey}
      onChange={(v) => range.setRangeKey(v as RangeKey)}
    />
  );
}
