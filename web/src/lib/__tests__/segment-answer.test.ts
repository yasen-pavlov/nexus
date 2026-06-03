import { describe, it, expect } from "vitest";

import type { ChatCitation } from "../api-types";
import { segmentAnswer } from "../segment-answer";

const cite = (start: number, end: number, doc = "doc-1"): ChatCitation => ({
  doc_id: doc,
  span_start: start,
  span_end: end,
});

describe("segmentAnswer", () => {
  it("returns empty for empty text", () => {
    expect(segmentAnswer("", [])).toEqual([]);
    expect(segmentAnswer("", [cite(0, 0)])).toEqual([]);
  });

  it("returns text-only when no citations", () => {
    expect(segmentAnswer("hello", [])).toEqual([
      { kind: "text", text: "hello" },
    ]);
  });

  it("anchors a single citation pill at span_end", () => {
    const out = segmentAnswer("Hello world.", [cite(0, 5)]);
    expect(out).toEqual([
      { kind: "text", text: "Hello" },
      { kind: "pill", citation: { doc_id: "doc-1", span_start: 0, span_end: 5 } },
      { kind: "text", text: " world." },
    ]);
  });

  it("renders adjacent citations in order", () => {
    const out = segmentAnswer("ABCDEF", [cite(0, 2, "a"), cite(2, 4, "b")]);
    expect(out).toEqual([
      { kind: "text", text: "AB" },
      { kind: "pill", citation: expect.objectContaining({ doc_id: "a" }) },
      { kind: "text", text: "CD" },
      { kind: "pill", citation: expect.objectContaining({ doc_id: "b" }) },
      { kind: "text", text: "EF" },
    ]);
  });

  it("defers citations whose span_end exceeds the text length", () => {
    const out = segmentAnswer("Hi", [cite(0, 5)]);
    expect(out).toEqual([{ kind: "text", text: "Hi" }]);
  });

  it("delivers a previously-deferred citation once text catches up", () => {
    const c = cite(0, 5);
    expect(segmentAnswer("Hi", [c])).toEqual([{ kind: "text", text: "Hi" }]);
    const out = segmentAnswer("Hello world", [c]);
    expect(out[0]).toEqual({ kind: "text", text: "Hello" });
    expect(out[1]).toEqual(expect.objectContaining({ kind: "pill" }));
  });

  it("sorts out-of-order citations by span_end", () => {
    const out = segmentAnswer("ABCDEFGH", [cite(0, 6, "late"), cite(0, 2, "early")]);
    expect(out[1]).toEqual(
      expect.objectContaining({ kind: "pill", citation: expect.objectContaining({ doc_id: "early" }) }),
    );
    expect(out[3]).toEqual(
      expect.objectContaining({ kind: "pill", citation: expect.objectContaining({ doc_id: "late" }) }),
    );
  });

  it("drops malformed citations defensively", () => {
    expect(segmentAnswer("hello", [cite(-1, 3)])).toEqual([
      { kind: "text", text: "hello" },
    ]);
    expect(segmentAnswer("hello", [cite(3, 1)])).toEqual([
      { kind: "text", text: "hello" },
    ]);
    expect(segmentAnswer("hello", [cite(99, 99)])).toEqual([
      { kind: "text", text: "hello" },
    ]);
  });

  it("renders trailing pills without a tail text segment", () => {
    const out = segmentAnswer("Hello", [cite(0, 5)]);
    expect(out).toEqual([
      { kind: "text", text: "Hello" },
      { kind: "pill", citation: expect.objectContaining({ span_end: 5 }) },
    ]);
  });

  it("handles two citations sharing the same span_end", () => {
    const out = segmentAnswer("ABCDEF", [cite(0, 4, "a"), cite(0, 4, "b")]);
    expect(out.filter((s) => s.kind === "pill")).toHaveLength(2);
  });
});
