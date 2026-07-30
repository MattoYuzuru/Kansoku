import { QueryClientProvider } from "@tanstack/react-query";
import { makeQueryClient } from "./api/client";
import { ThemeProvider } from "./theme";
import { AppShell } from "./AppShell";
import { AppRoutes } from "./routes";
import { ErrorBoundary } from "./components/ErrorBoundary";
import { QueryErrorState } from "./components/QueryErrorState";

const queryClient = makeQueryClient();

export function App() {
  return (
    <ErrorBoundary
      fallback={({ retry }) => (
        <main style={{ padding: "var(--space-5)" }}>
          <QueryErrorState
            title="Kansoku could not start"
            subject="the dashboard shell"
            onRetry={retry}
            backHref="/"
          />
        </main>
      )}
    >
      <QueryClientProvider client={queryClient}>
        <ThemeProvider>
          <AppShell>
            <AppRoutes />
          </AppShell>
        </ThemeProvider>
      </QueryClientProvider>
    </ErrorBoundary>
  );
}
