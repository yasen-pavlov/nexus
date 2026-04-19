import { test as base } from "@playwright/test";
import fs from "node:fs";
import path from "node:path";
import crypto from "node:crypto";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
// Each Playwright test dumps its raw V8 coverage as a JSON array here.
// The merge script later feeds these through monocart's `.add()` API.
const outDir = path.resolve(here, "..", ".v8-coverage", "e2e");

// Capture V8 JS coverage from Chromium for each Playwright test.
// Chromium-only — Firefox/WebKit don't expose a V8 coverage API. The raw
// entries get written to .v8-coverage/e2e/ for monocart to merge with the
// vitest-side coverage in the final report.
export const test = base.extend<{ collectCoverage: void }>({
  collectCoverage: [
    async ({ page, browserName }, use) => {
      const canCollect = browserName === "chromium";
      if (canCollect) {
        await page.coverage.startJSCoverage({
          resetOnNavigation: false,
          reportAnonymousScripts: false,
        });
      }
      await use();
      if (!canCollect) return;
      try {
        const entries = await page.coverage.stopJSCoverage();
        // Keep only bundles served from our dev server — drops node_modules
        // pre-bundles and Vite client runtime that aren't our source.
        const relevant = entries.filter((e) => {
          if (!e.source) return false;
          if (!e.url.includes("/src/")) return false;
          if (e.url.includes("/node_modules/")) return false;
          if (e.url.includes("/@vite/") || e.url.includes("/@fs/")) return false;
          // Dev server serves CSS modules from the same origin — only take
          // JS/TS/TSX entries so monocart's JS converter doesn't choke on
          // CSS ast nodes.
          return /\.(m?jsx?|tsx?)($|\?)/.test(e.url);
        });
        if (relevant.length > 0) {
          fs.mkdirSync(outDir, { recursive: true });
          // Plain array of V8 coverage entries — monocart's .add() API
          // consumes this shape directly (no envelope needed).
          const payload = relevant.map((e) => ({
            url: e.url,
            scriptId: e.scriptId,
            source: e.source,
            functions: e.functions ?? e.rawScriptCoverage?.functions ?? [],
          }));
          fs.writeFileSync(
            path.join(outDir, `e2e-${crypto.randomUUID()}.json`),
            JSON.stringify(payload),
          );
        }
      } catch {
        // page closed / browser gone — nothing to collect
      }
    },
    { auto: true },
  ],
});

export { expect, type Page } from "@playwright/test";
