import { Link } from "wouter";
import "./QueryErrorState.css";

interface QueryErrorStateProps {
  title?: string;
  subject: string;
  onRetry: () => void;
  backHref?: string;
  onBack?: () => void;
}

export function QueryErrorState({
  title = "Data unavailable",
  subject,
  onRetry,
  backHref,
  onBack,
}: QueryErrorStateProps) {
  return (
    <section className="k-query-error" role="alert">
      <h1 className="t-page-title">{title}</h1>
      <p className="t-body">
        Kansoku could not load {subject}. Existing telemetry was not changed.
      </p>
      <div className="k-query-error__actions">
        <button type="button" onClick={onRetry}>Retry</button>
        {backHref && <Link href={backHref}>Back</Link>}
        {!backHref && onBack && <button type="button" onClick={onBack}>Back</button>}
      </div>
    </section>
  );
}
