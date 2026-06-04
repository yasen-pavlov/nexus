import { describe, expect, it } from "vitest";

import { sanitizeHighlight } from "@/lib/sanitize-highlight";

describe("sanitizeHighlight", () => {
  it("keeps the highlight tags we emit", () => {
    expect(sanitizeHighlight("a <mark>hit</mark> here")).toBe(
      "a <mark>hit</mark> here",
    );
    expect(sanitizeHighlight("an <em>emphasis</em>")).toBe(
      "an <em>emphasis</em>",
    );
  });

  it("neutralizes a script-injecting image tag (no live tag forms)", () => {
    const out = sanitizeHighlight(
      `before <img src=x onerror="fetch('//evil')"> after`,
    );
    // The dangerous tag must not survive as live markup...
    expect(out).not.toContain("<img");
    expect(out).not.toContain("<");
    // ...it is escaped to inert text instead.
    expect(out).toContain("&lt;img");
    expect(out).toContain("before");
    expect(out).toContain("after");
  });

  it("neutralizes script tags", () => {
    const out = sanitizeHighlight(`x<script>alert(1)</script>y`);
    expect(out).not.toContain("<script");
    expect(out).not.toContain("<");
    expect(out).toContain("&lt;script&gt;");
  });

  it("does not restore highlight tags that carry attributes", () => {
    const out = sanitizeHighlight(`<mark onclick="steal()">hit</mark>`);
    // The opening tag has an attribute, so it stays escaped (inert);
    // only the exact attribute-free <mark>/</mark> forms are restored.
    expect(out).not.toContain("<mark onclick");
    expect(out).toContain("&lt;mark onclick");
  });

  it("does not double-escape content the server already escaped", () => {
    expect(sanitizeHighlight("price &lt;5 and &gt;1")).toBe(
      "price &lt;5 and &gt;1",
    );
  });

  it("preserves plain text untouched", () => {
    expect(sanitizeHighlight("just text, no markup")).toBe(
      "just text, no markup",
    );
  });
});
