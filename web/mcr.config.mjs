// Config for vitest-monocart-coverage (the custom provider plugged into
// vitest's `coverage` block). We only want this run to emit raw V8 entries
// into .v8-coverage/unit/ so the final merge script can combine them with
// the Playwright-side raw V8 dumps.

export default {
  name: "Nexus Unit Coverage",
  reports: ["raw"],
  outputDir: "./.v8-coverage/unit",
  entryFilter: {
    "**/node_modules/**": false,
    "**/src/test/**": false,
    "**/*.test.*": false,
    "**/routeTree.gen.ts": false,
    "**/src/**": true,
  },
  sourceFilter: {
    "**/node_modules/**": false,
    "**/src/test/**": false,
    "**/*.test.*": false,
    "**/routeTree.gen.ts": false,
    "**/src/**": true,
  },
};
