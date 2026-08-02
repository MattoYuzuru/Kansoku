import type { ReactNode } from "react";
import { Link } from "wouter";
import { GLOSSARY_BY_ID } from "../generated/glossary";
import { Icon } from "../ui/icons";
import "./GlossaryTerm.css";

export function GlossaryTerm({ id, children }: { id: string; children: ReactNode }) {
  const term = GLOSSARY_BY_ID.get(id);
  return (
    <span className="k-term">
      <span>{children}</span>
      <Link
        className="k-term__link"
        href={`/glossary#${encodeURIComponent(id)}`}
        title={term?.plainDefinition ?? `Open the definition of ${id}`}
        aria-label={`Open definition: ${String(children)}`}
      >
        <Icon name="info-circle" size={14} />
      </Link>
    </span>
  );
}
