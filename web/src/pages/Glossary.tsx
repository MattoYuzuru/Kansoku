import { useEffect, useMemo, useState } from "react";
import { GLOSSARY_TERMS } from "../generated/glossary";
import { Panel } from "../components/Panel";
import "./Glossary.css";

function titleFor(id: string): string {
  return id
    .split("_")
    .map((part) => part.charAt(0).toUpperCase() + part.slice(1))
    .join(" ");
}

export function Glossary() {
  const [query, setQuery] = useState("");
  const terms = useMemo(() => {
    const needle = query.trim().toLocaleLowerCase();
    if (!needle) return GLOSSARY_TERMS;
    return GLOSSARY_TERMS.filter((term) =>
      [term.id, term.definition, term.plainDefinition]
        .join("\n")
        .toLocaleLowerCase()
        .includes(needle),
    );
  }, [query]);

  useEffect(() => {
    if (!window.location.hash) return;
    const id = decodeURIComponent(window.location.hash.slice(1));
    requestAnimationFrame(() => document.getElementById(id)?.scrollIntoView({ block: "center" }));
  }, []);

  return (
    <section className="k-page">
      <header className="k-page__head">
        <h1 className="t-page-title">Glossary</h1>
        <p className="k-page__wire t-caption">
          Plain-language definitions for metrics, evidence states and local runtime operations.
        </p>
      </header>

      <Panel
        title="Product language"
        caption={`${terms.length} of ${GLOSSARY_TERMS.length} terms. Definitions are generated from the repository contract.`}
      >
        <label className="k-glossary__search">
          <span className="t-section-header">Search definitions</span>
          <input
            type="search"
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder="Try invoked, cold, fsync, database budget…"
          />
        </label>

        <div className="k-glossary__grid" aria-live="polite">
          {terms.map((term) => (
            <article className="k-glossary__term" id={term.id} key={term.id}>
              <h2 className="t-section-header">{titleFor(term.id)}</h2>
              <p className="t-body">{term.plainDefinition}</p>
              {term.plainDefinition !== term.definition && (
                <details>
                  <summary className="t-caption">Technical contract wording</summary>
                  <p className="t-caption">{term.definition}</p>
                </details>
              )}
            </article>
          ))}
        </div>

        {terms.length === 0 && (
          <p className="t-body k-glossary__empty">No definition matches “{query}”.</p>
        )}
      </Panel>
    </section>
  );
}
