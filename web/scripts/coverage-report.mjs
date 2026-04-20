#!/usr/bin/env node
// Merge V8 coverage from the vitest side (.v8-coverage/unit/raw, already in
// monocart's `raw` format) with the Playwright side (.v8-coverage/e2e, plain
// arrays of V8 entries) into a single source-mapped report.
//
// Both inputs are raw V8 — no statementMap/hash mismatch like the old
// Istanbul pipeline had. Monocart converts each entry through its inline
// sourcemap back to the on-disk source and sums hits per file.

import MCR from "monocart-coverage-reports";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const root = path.resolve(here, "..");

const mcr = MCR({
  name: "Nexus Combined Coverage",
  inputDir: [path.join(root, ".v8-coverage/unit/raw")],
  outputDir: path.join(root, "coverage/combined"),
  reports: [
    "v8",
    "v8-json",
    "console-summary",
    "console-details",
    "html",
    "lcovonly",
  ],
  entryFilter: {
    "**/node_modules/**": false,
    "**/src/test/**": false,
    "**/*.test.*": false,
    "**/routeTree.gen.ts": false,
    "**/src/**": true,
  },
  // Vite's dev-server sourcemaps only keep the filename in `sources` (e.g.
  // `stats.tsx` instead of `src/routes/.../stats.tsx`), so a `**/src/**`
  // glob drops every e2e source. A function filter side-steps the whole
  // glob matching issue — keep everything that isn't a node_modules / test
  // / generated file.
  sourceFilter: (sourcePath) => {
    if (sourcePath.includes("node_modules/")) return false;
    if (sourcePath.includes("src/test/")) return false;
    if (/\.test\.[jt]sx?$/.test(sourcePath)) return false;
    if (sourcePath.endsWith("routeTree.gen.ts")) return false;
    return true;
  },
  cleanCache: true,
  // Coverage floor — fails CI if any dimension drops below. Values sit a
  // few pp under the current run so routine churn doesn't break the build,
  // but real regressions (ripping out tests, untested new code paths) do.
  // Bump these in lockstep when the combined report moves up durably.
  onEnd: (results) => {
    const thresholds = {
      statements: 85,
      lines: 90,
      functions: 75,
      branches: 70,
    };
    const { summary } = results;
    const failures = [];
    for (const [k, floor] of Object.entries(thresholds)) {
      const pct = summary[k]?.pct ?? 0;
      if (pct < floor) {
        failures.push(
          `  ${k.padEnd(11)} ${pct.toFixed(2)}% < ${floor}% floor`,
        );
      }
    }
    if (failures.length > 0) {
      console.error(
        "\n[coverage:floor] Combined coverage below threshold:\n" +
          failures.join("\n") +
          "\n",
      );
      process.exit(1);
    }
    console.log(
      "\n[coverage:floor] All thresholds met " +
        `(statements ${summary.statements.pct.toFixed(2)}%, ` +
        `lines ${summary.lines.pct.toFixed(2)}%, ` +
        `functions ${summary.functions.pct.toFixed(2)}%, ` +
        `branches ${summary.branches.pct.toFixed(2)}%).\n`,
    );
  },
});

// Feed in the Playwright-side V8 dumps via .add() so monocart can pull
// `source` + inline sourcemap off each entry and merge with the vitest
// data loaded from inputDir.
//
// The Playwright side captures coverage against the Vite dev server, so
// urls look like `http://localhost:5174/src/foo.tsx`. Monocart derives
// `sourcePath` from the URL — unit's `file:///abs/src/foo.tsx` becomes
// `src/foo.tsx`, whereas `http://localhost:5174/src/foo.tsx` becomes
// `localhost-5174/src/foo.tsx`. Different sourcePaths for the same file
// mean the two coverage streams never merge. Rewrite the URL to a file://
// path so the sourcePath lines up.
const serverOrigin = "http://localhost:5174";

// Convert "http://localhost:5174/src/foo.tsx" → "file:///abs/path/src/foo.tsx"
// and rewrite the inline sourcemap's `sources` array from the basename-only
// form Vite emits (e.g. `foo.tsx`) into the matching absolute file path.
// Monocart keys merged coverage by the resolved source path, so without the
// rewrite unit and e2e coverage for the same file end up in two separate
// trees and don't combine.
const rewriteEntry = (entry) => {
  if (!entry.url.startsWith(serverOrigin)) return entry;
  const rel = entry.url.slice(serverOrigin.length).split("?")[0];
  const absPath = path.join(root, rel);
  const fileUrl = `file://${absPath}`;

  let source = entry.source ?? "";
  const smMatch = source.match(
    /\/\/# sourceMappingURL=data:application\/json;(?:charset=[^;]+;)?base64,([A-Za-z0-9+/=]+)\s*$/,
  );
  if (smMatch) {
    try {
      const decoded = Buffer.from(smMatch[1], "base64").toString("utf-8");
      const sm = JSON.parse(decoded);
      // The basename stored in `sources[0]` is the on-disk file's basename;
      // rewrite to the absolute path so monocart's sourcePath matches the
      // unit-side (which already uses absolute-looking paths).
      if (Array.isArray(sm.sources)) {
        sm.sources = sm.sources.map((s) => {
          if (!s || path.isAbsolute(s)) return s;
          if (path.basename(absPath) === s) return absPath;
          return s;
        });
      }
      const reencoded = Buffer.from(JSON.stringify(sm)).toString("base64");
      source = source.replace(
        smMatch[0],
        `//# sourceMappingURL=data:application/json;base64,${reencoded}`,
      );
    } catch {
      // leave source untouched on parse error
    }
  }
  return { ...entry, url: fileUrl, source };
};

const e2eDir = path.join(root, ".v8-coverage/e2e");
if (fs.existsSync(e2eDir)) {
  const files = fs
    .readdirSync(e2eDir)
    .filter((f) => f.endsWith(".json"))
    .map((f) => path.join(e2eDir, f));
  for (const f of files) {
    const entries = JSON.parse(fs.readFileSync(f, "utf-8")).map(rewriteEntry);
    await mcr.add(entries);
  }
  console.log(`[report] added ${files.length} Playwright coverage dumps`);
}

await mcr.generate();
