import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import { fileURLToPath, URL } from "node:url";

const implementation = process.env.VITE_CHART_IMPL === "uplot" ? "AppUPlot.jsx" : "AppECharts.jsx";

export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      "@chart-app": fileURLToPath(new URL(`./src/${implementation}`, import.meta.url)),
    },
  },
  build: {
    sourcemap: false,
    target: "es2022",
    reportCompressedSize: true,
  },
});
