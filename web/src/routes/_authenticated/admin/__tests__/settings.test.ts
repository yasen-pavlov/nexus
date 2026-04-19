import { describe, expect, it } from "vitest";
import { scoreActiveSection } from "../settings";

describe("scoreActiveSection", () => {
  const offset = 80;
  const viewportHeight = 600;

  it("returns null for an empty positions list", () => {
    expect(scoreActiveSection([], viewportHeight, false, offset)).toBeNull();
  });

  it("picks the most-recently-passed section when scrolled past the offset", () => {
    const positions = [
      { id: "a", top: -200 },
      { id: "b", top: -50 },
      { id: "c", top: 300 },
    ];
    expect(scoreActiveSection(positions, viewportHeight, false, offset)).toBe(
      "b",
    );
  });

  it("falls back to the first section when nothing has crossed the offset yet", () => {
    const positions = [
      { id: "a", top: 200 },
      { id: "b", top: 500 },
    ];
    expect(scoreActiveSection(positions, viewportHeight, false, offset)).toBe(
      "a",
    );
  });

  it("at-bottom picks the bottom-most visible section when no header has crossed", () => {
    const positions = [
      { id: "a", top: 100 },
      { id: "b", top: 250 },
      { id: "c", top: 400 },
    ];
    expect(scoreActiveSection(positions, viewportHeight, true, offset)).toBe(
      "c",
    );
  });

  it("at-bottom still prefers the most-recently-passed when everything visible is off-screen", () => {
    const positions = [
      { id: "a", top: -500 },
      { id: "b", top: -100 },
      { id: "c", top: 700 }, // outside viewport (700 > 600)
    ];
    expect(scoreActiveSection(positions, viewportHeight, true, offset)).toBe(
      "b",
    );
  });
});
