/*
 * ScrollArea — a thin wrapper that opts an element into the §5 CSS-only styled
 * native scrollbar. No JS overlay, so it keeps native momentum scrolling and
 * adds zero runtime DOM/JS. The scrollbar styling itself is global (base.css);
 * this component just provides the overflow container and reduced-motion-safe
 * smooth-scroll opt-in.
 */
import type { CSSProperties, ReactNode } from "react";
import "./ScrollArea.css";

export interface ScrollAreaProps {
  children: ReactNode;
  /** "both" | "x" | "y" — which axes may overflow-scroll. */
  axis?: "both" | "x" | "y";
  className?: string;
  style?: CSSProperties;
}

export function ScrollArea({ children, axis = "y", className, style }: ScrollAreaProps) {
  return (
    <div className={`k-scroll k-scroll--${axis}${className ? " " + className : ""}`} style={style}>
      {children}
    </div>
  );
}
