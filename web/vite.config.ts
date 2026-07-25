import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// Kansoku dashboard build. No CDN, no runtime network beyond the same-origin
// /api/v1 surface. Everything (fonts, icons, chart lib) is bundled and served
// by the Go binary that embeds web/dist. index.html is emitted as a template
// (a Go text/template) so the runtime can inject the read-token / CSRF meta
// tags per request; see internal/webui.
export default defineConfig({
  plugins: [react()],
  build: {
    // Emitted assets are committed and embedded into the Go binary via
    // internal/webui (go:embed). Keep names stable-hashed for cache-busting.
    outDir: "dist",
    emptyOutDir: true,
    target: "es2022",
    cssCodeSplit: true,
    // Report chunk sizes; the shell chunk must stay well under the analytics
    // 250 KiB gzip budget shared with React + ECharts (ADR 0001 spike).
    chunkSizeWarningLimit: 260,
    rollupOptions: {
      output: {
        // Split heavy libraries into their own chunks so the initial shell
        // (React + wouter + tokens + sidebar) loads without ECharts or the
        // full TanStack Query surface. Per-route pages lazy-load via import().
        manualChunks(id) {
          if (id.includes("node_modules/echarts") || id.includes("node_modules/zrender")) {
            return "echarts";
          }
          if (id.includes("node_modules/@tanstack")) {
            return "query";
          }
          return undefined;
        },
      },
    },
  },
});
