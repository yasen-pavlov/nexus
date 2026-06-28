import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import { TanStackRouterVite } from "@tanstack/router-plugin/vite";
import path from "node:path";

export default defineConfig({
  plugins: [
    TanStackRouterVite({
      routesDirectory: "./src/routes",
      generatedRouteTree: "./src/routeTree.gen.ts",
      routeFileIgnorePattern: String.raw`(\.test\.|__tests__)`,
      // Split each route's component/loader into its own chunk so the initial
      // bundle only ships the current route (chat pulls react-markdown, admin
      // pulls react-table, etc. — no reason to load them all upfront). Disabled
      // under vitest (which merges this config): code-splitting makes route
      // components load async, adding ticks that flake the unit tests' waitFor
      // renders, and it buys tests nothing.
      autoCodeSplitting: !process.env.VITEST,
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
});
