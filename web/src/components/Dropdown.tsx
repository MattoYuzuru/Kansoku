/*
 * Dropdown / Combobox (§6). No native <select> anywhere: an ARIA
 * combobox/listbox with full keyboard operability.
 *
 * Keyboard (§6):
 *   Enter/Space/ArrowDown  open
 *   ArrowUp/ArrowDown      move active option (wraps)
 *   Home/End               jump to first/last
 *   Enter                  select active + close
 *   Escape                 close without change
 *   typeahead              label-prefix match within a 500ms buffer
 * Focus is trapped in the open listbox; aria-activedescendant tracks the active
 * option. Multi-select variant uses checkbox-style toggles + a trailing count
 * chip. Open/close motion is §4 #9 (opacity + scaleY + translateY), CSS-driven.
 */
import { useCallback, useEffect, useId, useMemo, useRef, useState } from "react";
import { Icon } from "../ui/icons";
import "./Dropdown.css";

export interface DropdownOption {
  value: string;
  label: string;
  disabled?: boolean;
}

interface BaseProps {
  /** Leading mono caption label above the trigger, e.g. "RANGE". */
  caption?: string;
  options: readonly DropdownOption[];
  placeholder?: string;
  className?: string;
  id?: string;
}

interface SingleProps extends BaseProps {
  multiple?: false;
  value: string | null;
  onChange: (value: string) => void;
}

interface MultiProps extends BaseProps {
  multiple: true;
  value: readonly string[];
  onChange: (value: string[]) => void;
}

export type DropdownProps = SingleProps | MultiProps;

const TYPEAHEAD_MS = 500;

export function Dropdown(props: DropdownProps) {
  const { caption, options, placeholder = "Select…", className } = props;
  const listboxId = useId();
  const [open, setOpen] = useState(false);
  const [active, setActive] = useState(0);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const listRef = useRef<HTMLUListElement>(null);
  const rootRef = useRef<HTMLElement>(null);
  const typeaheadRef = useRef<{ buffer: string; at: number }>({ buffer: "", at: 0 });

  const selectedValues = useMemo<Set<string>>(
    () =>
      props.multiple
        ? new Set(props.value)
        : props.value !== null
          ? new Set([props.value])
          : new Set<string>(),
    [props],
  );

  const triggerLabel = useMemo(() => {
    if (props.multiple) {
      if (props.value.length === 0) return placeholder;
      // The count of additional selections is shown in the trailing count chip.
      return options.find((o) => o.value === props.value[0])?.label ?? props.value[0];
    }
    if (props.value === null) return placeholder;
    return options.find((o) => o.value === props.value)?.label ?? props.value;
  }, [props, options, placeholder]);

  const commitSelect = useCallback(
    (opt: DropdownOption) => {
      if (opt.disabled) return;
      if (props.multiple) {
        const set = new Set(props.value);
        if (set.has(opt.value)) set.delete(opt.value);
        else set.add(opt.value);
        props.onChange([...set]);
        // multi-select keeps the listbox open
      } else {
        props.onChange(opt.value);
        setOpen(false);
        triggerRef.current?.focus();
      }
    },
    [props],
  );

  // Move focus into the list when opening; restore to trigger on close.
  useEffect(() => {
    if (open) {
      const idx = options.findIndex((o) => selectedValues.has(o.value));
      setActive(idx >= 0 ? idx : 0);
      listRef.current?.focus();
    }
  }, [open, options, selectedValues]);

  // Close on outside click.
  useEffect(() => {
    if (!open) return;
    const onDocClick = (e: MouseEvent) => {
      if (rootRef.current && !rootRef.current.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener("mousedown", onDocClick);
    return () => document.removeEventListener("mousedown", onDocClick);
  }, [open]);

  const moveActive = useCallback(
    (dir: 1 | -1) => {
      setActive((prev) => {
        const n = options.length;
        let next = prev;
        for (let i = 0; i < n; i++) {
          next = (next + dir + n) % n;
          if (!options[next]?.disabled) break;
        }
        return next;
      });
    },
    [options],
  );

  const onListKeyDown = useCallback(
    (e: React.KeyboardEvent) => {
      switch (e.key) {
        case "ArrowDown":
          e.preventDefault();
          moveActive(1);
          break;
        case "ArrowUp":
          e.preventDefault();
          moveActive(-1);
          break;
        case "Home":
          e.preventDefault();
          setActive(0);
          break;
        case "End":
          e.preventDefault();
          setActive(options.length - 1);
          break;
        case "Enter":
        case " ":
          e.preventDefault();
          if (options[active]) commitSelect(options[active]);
          break;
        case "Escape":
          e.preventDefault();
          setOpen(false);
          triggerRef.current?.focus();
          break;
        case "Tab":
          setOpen(false);
          break;
        default:
          // typeahead prefix match within a 500ms buffer
          if (e.key.length === 1) {
            const now = performance.now();
            const ta = typeaheadRef.current;
            ta.buffer = now - ta.at > TYPEAHEAD_MS ? e.key : ta.buffer + e.key;
            ta.at = now;
            const idx = options.findIndex((o) =>
              o.label.toLowerCase().startsWith(ta.buffer.toLowerCase()),
            );
            if (idx >= 0) setActive(idx);
          }
      }
    },
    [active, commitSelect, moveActive, options],
  );

  const onTriggerKeyDown = useCallback((e: React.KeyboardEvent) => {
    if (e.key === "Enter" || e.key === " " || e.key === "ArrowDown") {
      e.preventDefault();
      setOpen(true);
    }
  }, []);

  const activeId = `${listboxId}-opt-${active}`;

  return (
    <div
      className={`k-dd${className ? " " + className : ""}`}
      ref={(node) => {
        rootRef.current = node;
      }}
    >
      {caption && <span className="k-dd__caption t-section-header">{caption}</span>}
      <button
        ref={triggerRef}
        type="button"
        className="k-dd__trigger"
        role="combobox"
        aria-expanded={open}
        aria-haspopup="listbox"
        aria-controls={listboxId}
        onClick={() => setOpen((o) => !o)}
        onKeyDown={onTriggerKeyDown}
      >
        <span className="k-dd__value">{triggerLabel}</span>
        {props.multiple && props.value.length > 1 && (
          <span className="k-dd__count">{props.value.length}</span>
        )}
        <span className={`k-dd__chevron${open ? " is-open" : ""}`} aria-hidden="true">
          <Icon name="chevron-down" size={16} />
        </span>
      </button>
      {open && (
        <ul
          ref={listRef}
          id={listboxId}
          className="k-dd__list"
          role="listbox"
          tabIndex={-1}
          aria-multiselectable={props.multiple || undefined}
          aria-activedescendant={activeId}
          onKeyDown={onListKeyDown}
        >
          {options.map((opt, i) => {
            const selected = selectedValues.has(opt.value);
            return (
              <li
                key={opt.value}
                id={`${listboxId}-opt-${i}`}
                role="option"
                aria-selected={selected}
                aria-disabled={opt.disabled || undefined}
                className={`k-dd__opt${i === active ? " is-active" : ""}${
                  selected ? " is-selected" : ""
                }${opt.disabled ? " is-disabled" : ""}`}
                onMouseEnter={() => setActive(i)}
                onClick={() => commitSelect(opt)}
              >
                <span className="k-dd__check" aria-hidden="true">
                  {selected && <Icon name="check" size={14} />}
                </span>
                <span className="k-dd__opt-label">{opt.label}</span>
              </li>
            );
          })}
        </ul>
      )}
    </div>
  );
}
