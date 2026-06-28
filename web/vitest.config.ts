import { defineConfig, mergeConfig } from "vitest/config";
import viteConfig from "./vite.config";

// vite.config exports a command-aware function; resolve it for the "serve"
// command so unit tests render routes eagerly (autoCodeSplitting stays off).
const base = viteConfig({ command: "serve", mode: "test" });

export default mergeConfig(
  base,
  defineConfig({
    test: {
      environment: "happy-dom",
      globals: true,
      setupFiles: ["./src/test/setup.ts"],
      include: ["src/**/*.test.{ts,tsx}"],
      coverage: {
        enabled: !!process.env.COVERAGE,
        provider: "custom",
        customProviderModule: "vitest-monocart-coverage",
        include: ["src/**/*.{ts,tsx}"],
        exclude: [
          "src/test/**",
          "src/**/*.test.{ts,tsx}",
          "src/routeTree.gen.ts",
          "src/**/*.d.ts",
        ],
      },
    },
  }),
);
