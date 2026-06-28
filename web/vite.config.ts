import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import { TanStackRouterVite } from "@tanstack/router-plugin/vite";
import path from "node:path";

export default defineConfig(({ command }) => ({
  plugins: [
    TanStackRouterVite({
      routesDirectory: "./src/routes",
      generatedRouteTree: "./src/routeTree.gen.ts",
      routeFileIgnorePattern: String.raw`(\.test\.|__tests__)`,
      // Per-route code-splitting is a production-bundle optimization: it keeps
      // the entry chunk small (~466 kB vs 1.4 MB) instead of bundling every
      // route upfront. Enable it ONLY for the build — in dev / e2e (vite serve)
      // and under vitest, routes stay eager so components mount synchronously
      // and don't race keyboard/async tests (the e2e dev server runs `vite dev`).
      autoCodeSplitting: command === "build",
    }),
    react(),
    tailwindcss(),
  ],
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "./src"),
    },
  },
  server: {
    port: 5174,
    proxy: {
      "/api": "http://localhost:8080",
    },
  },
}));
