// Headless probe that samples the conversation scroller's scrollTop and
// reports any rapid oscillation — the "vibrating" the user described.
import { chromium } from "playwright";
import fs from "fs";

const TOKEN = fs.readFileSync("/tmp/admin-token.txt", "utf8").trim();

const browser = await chromium.launch({
  executablePath: process.env.HOME + "/.cache/ms-playwright/chromium-1217/chrome-linux64/chrome",
});
const ctx = await browser.newContext({
  viewport: { width: 1200, height: 800 },
});
const page = await ctx.newPage();

page.on("console", (msg) => {
  if (msg.type() === "error") console.log("BROWSER-ERROR:", msg.text());
});
page.on("pageerror", (err) => console.log("BROWSER-PAGEERROR:", err.message));

// Log a fake token into localStorage, then navigate to the conversation.
await page.addInitScript((tok) => {
  localStorage.setItem("nexus_jwt", tok);
}, TOKEN);

// Navigate with a real anchor.
const url = `http://localhost:5174/conversations/telegram/596632019?anchor_id=230065&anchor_ts=${encodeURIComponent("2026-04-13T17:31:29+02:00")}`;
console.log("navigating to:", url);
await page.goto(url, { waitUntil: "domcontentloaded" });

// Wait for the scroller.
await page.waitForSelector("[data-conversation-scroll]", { timeout: 5000 });

// Sample scrollTop every 50ms for 5 seconds.
const samples = await page.evaluate(async () => {
  const el = document.querySelector("[data-conversation-scroll]");
  const out = [];
  const start = performance.now();
  while (performance.now() - start < 5000) {
    out.push({
      t: Math.round(performance.now() - start),
      scrollTop: el.scrollTop,
      scrollHeight: el.scrollHeight,
      clientHeight: el.clientHeight,
      msgCount: document.querySelectorAll('[id^="msg-"]').length,
    });
    await new Promise((r) => setTimeout(r, 50));
  }
  return out;
});

console.log(`captured ${samples.length} samples`);
// Find oscillations — absolute scrollTop delta between adjacent samples.
let maxJump = 0;
let jumpCount = 0;
for (let i = 1; i < samples.length; i++) {
  const d = Math.abs(samples[i].scrollTop - samples[i - 1].scrollTop);
  if (d > 50) jumpCount++;
  if (d > maxJump) maxJump = d;
}
console.log(`max jump (50ms window): ${maxJump}px`);
console.log(`jumps > 50px: ${jumpCount}`);
console.log("first 10:", samples.slice(0, 10));
console.log("last 10:", samples.slice(-10));
console.log("final msg count:", samples.at(-1)?.msgCount);

// Are there multiple fetches?
const networkFetches = [];
page.on("request", (req) => {
  if (req.url().includes("/api/conversations/")) networkFetches.push(req.url());
});

await browser.close();
