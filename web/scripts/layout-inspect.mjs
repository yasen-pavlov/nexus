import { chromium } from "playwright";
import fs from "fs";

const TOKEN = fs.readFileSync("/tmp/admin-token.txt", "utf8").trim();
const browser = await chromium.launch({
  executablePath: process.env.HOME + "/.cache/ms-playwright/chromium-1217/chrome-linux64/chrome",
});
const ctx = await browser.newContext({ viewport: { width: 1200, height: 800 } });
const page = await ctx.newPage();
await page.addInitScript((tok) => localStorage.setItem("nexus_jwt", tok), TOKEN);
await page.goto(
  `http://localhost:5174/conversations/telegram/596632019?anchor_id=230065&anchor_ts=${encodeURIComponent("2026-04-13T17:31:29+02:00")}`,
  { waitUntil: "domcontentloaded" },
);
await page.waitForSelector("[data-conversation-scroll]", { timeout: 5000 });

const chain = await page.evaluate(() => {
  const out = [];
  const inspect = (el, label) => {
    const cs = getComputedStyle(el);
    out.push({
      label,
      tag: el.tagName,
      cls: (el.className?.toString() || "").slice(0, 100),
      h: cs.height,
      minH: cs.minHeight,
      display: cs.display,
      flexGrow: cs.flexGrow,
      offsetH: el.offsetHeight,
    });
  };
  inspect(document.documentElement, "html");
  inspect(document.body, "body");
  const scroller = document.querySelector("[data-conversation-scroll]");
  let el = scroller;
  while (el && el !== document.body) {
    inspect(el, "");
    el = el.parentElement;
  }
  return out;
});

for (const e of chain) {
  console.log(`${e.label || e.tag}.${e.cls.slice(0, 70)}`);
  console.log(`  h=${e.h} minH=${e.minH} display=${e.display} flexGrow=${e.flexGrow} offsetH=${e.offsetH}`);
}

await browser.close();
