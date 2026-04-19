import { describe, expect, it } from "vitest";
import { formatBytes, formatCount, formatRelative } from "../format";

describe("formatBytes", () => {
  it("em-dashes invalid input", () => {
    expect(formatBytes(Number.NaN)).toBe("—");
    expect(formatBytes(-1)).toBe("—");
  });

  it("renders 0 as '0 B'", () => {
    expect(formatBytes(0)).toBe("0 B");
  });

  it("picks the unit that lands the value in [0.1, 1000)", () => {
    expect(formatBytes(512)).toBe("512 B");
    expect(formatBytes(1024)).toBe("1.0 KB");
    expect(formatBytes(1024 * 1024)).toBe("1.0 MB");
    expect(formatBytes(1024 * 1024 * 1024)).toBe("1.0 GB");
  });

  it("drops decimals past the threshold where value >= 10", () => {
    // 102 MiB rounds cleanly, 12 KB etc.
    expect(formatBytes(100 * 1024)).toBe("100 KB");
    expect(formatBytes(1536)).toBe("1.5 KB");
  });
});

describe("formatCount", () => {
  it("em-dashes invalid input", () => {
    expect(formatCount(Number.NaN)).toBe("—");
    expect(formatCount(Infinity)).toBe("—");
  });

  it("adds the locale thousands separator", () => {
    expect(formatCount(1)).toBe("1");
    expect(formatCount(1000)).toMatch(/1[,.\u202f ]000/);
  });
});

describe("formatRelative", () => {
  it("em-dashes empty input", () => {
    expect(formatRelative(undefined)).toBe("—");
    expect(formatRelative(null)).toBe("—");
    expect(formatRelative("")).toBe("—");
  });

  it("returns a human-readable relative string for a valid iso", () => {
    const iso = new Date(Date.now() - 60 * 1000).toISOString();
    const s = formatRelative(iso);
    expect(s).toMatch(/ago$/);
    expect(s).not.toBe(iso);
  });

  it("falls back to the raw value on unparseable input", () => {
    expect(formatRelative("not-a-date")).toBe("not-a-date");
  });
});
