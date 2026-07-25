import { QueryClientProvider } from "@tanstack/react-query";
import { makeQueryClient } from "./api/client";
import { ThemeProvider } from "./theme";
import { AppShell } from "./AppShell";
import { AppRoutes } from "./routes";

const queryClient = makeQueryClient();

export function App() {
  return (
    <QueryClientProvider client={queryClient}>
      <ThemeProvider>
        <AppShell>
          <AppRoutes />
        </AppShell>
      </ThemeProvider>
    </QueryClientProvider>
  );
}
