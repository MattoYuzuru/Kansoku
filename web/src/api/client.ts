/*
 * Same-origin /api/v1 client (ADR 0013 + internal/localhttp/security.go).
 *
 * The whole appliance is loopback-only and this bundle is served by the same
 * Go process on :43100. There is no login screen: the Go index handler injects
 * the read-bearer + CSRF tokens into <meta> tags per request, and this client
 * reads them once at startup and attaches `Authorization: Bearer <read-token>`
 * to every /api/v1 fetch. The dashboard is read-only (all 14 routes are GET),
 * so the mutation bearer is never embedded and no X-Kansoku-CSRF header is sent.
 *
 * Tokens live only in this module's memory for the page's lifetime — never
 * logged, never placed in a URL, never persisted.
 */
import { QueryClient } from "@tanstack/react-query";

const READ_TOKEN_META = "kansoku-read-token";
const CSRF_TOKEN_META = "kansoku-csrf-token";

function readMeta(name: string): string {
  const selector = `meta[name="${name}"]`;
  const el: HTMLMetaElement | null = document.querySelector(selector);
  return el ? el.content : "";
}

// Captured once at module load. Kept in a closure, not exported.
const readToken = readMeta(READ_TOKEN_META);
// The CSRF token is read (per the injection contract) but unused by this
// read-only build; retained here so a future mutation client can pick it up
// without touching the index template.
const csrfToken = readMeta(CSRF_TOKEN_META);
void csrfToken;

export const API_VERSION = "kansoku.api/1";

/** The 8 formal view-states from contracts/dashboard.yaml, plus loading. */
export type ViewState =
  | "loading"
  | "complete"
  | "partial"
  | "degraded"
  | "unsupported"
  | "not_observed"
  | "redacted"
  | "unknown"
  | "numeric_zero";

export interface Completeness {
  status?: string;
  /** API envelope summary uses `completeness`; metric payloads use `status`. */
  completeness?: string;
  numerator?: number;
  denominator?: number;
  exclusions?: string[];
  covered_ratio?: number;
  intervals?: string[];
}

/** APIEnvelope from internal/runtime/api.go. */
export interface ApiEnvelope<T> {
  api_version: string;
  request_id: string;
  data?: T;
  completeness?: Completeness;
  error?: string;
}

export class ApiError extends Error {
  constructor(
    readonly status: number,
    readonly category: string,
  ) {
    super(category);
    this.name = "ApiError";
  }
}

/**
 * Fetch a read (GET) /api/v1 endpoint. `path` must start with /api/v1/.
 * Query params are passed as a plain object so tokens never leak into URLs.
 */
export async function apiGet<T>(
  path: string,
  params?: Record<string, string | number | undefined>,
  signal?: AbortSignal,
): Promise<ApiEnvelope<T>> {
  if (!path.startsWith("/api/v1/")) {
    throw new Error("apiGet path must start with /api/v1/");
  }
  const url = new URL(path, window.location.origin);
  if (params) {
    for (const [k, v] of Object.entries(params)) {
      if (v !== undefined) url.searchParams.set(k, String(v));
    }
  }
  const res = await fetch(url.toString(), {
    method: "GET",
    headers: readToken ? { Authorization: `Bearer ${readToken}` } : {},
    credentials: "same-origin",
    signal,
  });
  let envelope: ApiEnvelope<T>;
  try {
    envelope = (await res.json()) as ApiEnvelope<T>;
  } catch {
    throw new ApiError(res.status, "invalid_response");
  }
  if (!res.ok || envelope.error) {
    throw new ApiError(res.status, envelope.error ?? `http_${res.status}`);
  }
  return envelope;
}

/**
 * Map a fetched envelope + its completeness.status into one of the 8 formal
 * view-states, so every page derives its render state identically (the TDD's
 * loading/stale/complete/partial/degraded/unsupported/unknown/zero model).
 *
 * `isEmptyMeasuredZero` lets a caller distinguish a real measured zero
 * (numeric_zero) from absence: pass true when the query succeeded and the
 * measured value is genuinely 0 rather than missing.
 */
export function deriveViewState(
  envelope: ApiEnvelope<unknown> | undefined,
  opts?: { isLoading?: boolean; isEmptyMeasuredZero?: boolean },
): ViewState {
  if (opts?.isLoading || envelope === undefined) return "loading";
  const status = envelope.completeness?.status ?? envelope.completeness?.completeness;
  switch (status) {
    case "complete":
    case "partial":
    case "degraded":
    case "unsupported":
    case "not_observed":
    case "redacted":
    case "numeric_zero":
      return status;
    default:
      break;
  }
  if (opts?.isEmptyMeasuredZero) return "numeric_zero";
  return "unknown";
}

/**
 * Shared TanStack Query client with the stale-while-revalidate defaults the TDD
 * requires: cached data renders immediately (stale) while a background refetch
 * updates it. Kept small; ECharts + query internals are code-split away from
 * the shell chunk (vite.config.ts manualChunks).
 */
export function makeQueryClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: {
        staleTime: 30_000, // serve cached (stale) data, revalidate in background
        gcTime: 5 * 60_000,
        refetchOnWindowFocus: true,
        refetchOnReconnect: true,
        retry: (failureCount, error) => {
          // Never retry auth/validation categories; retry transient 5xx twice.
          if (error instanceof ApiError && error.status < 500) return false;
          return failureCount < 2;
        },
      },
    },
  });
}
