/*
 * Switch — accessible toggle control. A real, visually-hidden
 * <input type="checkbox"> drives the state so it's fully keyboard/AT
 * operable for free; the visible track/thumb are plain sibling <span>s
 * styled off the input's :checked/:focus-visible state (see Switch.css) —
 * no extra JS state to keep in sync.
 */
import "./Switch.css";

export interface SwitchProps {
  checked: boolean;
  onChange: (checked: boolean) => void;
  label: string;
  id?: string;
}

export function Switch({ checked, onChange, label, id }: SwitchProps) {
  return (
    <label className="k-switch" htmlFor={id}>
      <input
        id={id}
        type="checkbox"
        className="k-switch__input"
        checked={checked}
        onChange={(e) => onChange(e.target.checked)}
      />
      <span className="k-switch__track" aria-hidden="true">
        <span className="k-switch__thumb" />
      </span>
      <span className="k-switch__label t-body">{label}</span>
    </label>
  );
}
