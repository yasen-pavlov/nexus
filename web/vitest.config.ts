import { defineConfig, mergeConfig } from "vitest/config";
import viteConfig from "./vite.config";

export default mergeConfig(
  viteConfig,
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
