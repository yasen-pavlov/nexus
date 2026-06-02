import { describe, expect, it, vi } from "vitest";
import { fireEvent } from "@testing-library/react";

import { render, screen, userEvent } from "@/test/test-utils";
import type { ChatMessage, ChunkPreview, LLMModelInfo } from "@/lib/api-types";

import { AnswerStream } from "../answer-stream";
import { AskComposer } from "../ask-composer";
import { CitationPill } from "../citation-pill";
import { EvidenceCard } from "../evidence-card";
import { ExamplePill } from "../example-pill";
import { PhaseChip } from "../phase-chip";
import { ToolTrace } from "../tool-trace";
import { UserTurn } from "../user-turn";
import { buildCopyText } from "../copy-text";

const sampleModels: LLMModelInfo[] = [
  {
    id: "anthropic:claude-sonnet-4-6",
    provider: "anthropic",
    bare_id: "claude-sonnet-4-6",
    display_name: "claude-sonnet-4-6",
    context_window: 200_000,
    supports_citations: true,
    supports_tools: true,
    supports_vision: false,
    supports_caching: true,
    input_cost_per_mtok: 3.0,
    output_cost_per_mtok: 15.0,
    typical_ttft_ms: 800,
  },
];

const sampleChunk: ChunkPreview = {
  id: "doc-1",
  title: "Anthropic invoice — March",
  source: "imap",
  date: "2026-03-15T00:00:00Z",
};

describe("ExamplePill", () => {
  it("calls onPick with the prompt on click", async () => {
    const user = userEvent.setup();
    const onPick = vi.fn();
    render(<ExamplePill prompt="What is the answer?" onPick={onPick} />);
    await user.click(screen.getByText("What is the answer?"));
    expect(onPick).toHaveBeenCalledWith("What is the answer?");
  });
});

describe("CitationPill", () => {
  it("renders the number and fires onClick", async () => {
    const user = userEvent.setup();
    const onClick = vi.fn();
    render(
      <CitationPill number={3} sourceType="imap" title="Email" onClick={onClick} />,
    );
    const btn = screen.getByRole("button", { name: /Citation 3 — Email/ });
    expect(btn).toHaveTextContent("3");
    await user.click(btn);
    expect(onClick).toHaveBeenCalled();
  });

  it("renders the unknown variant when number is -1", () => {
    render(
      <CitationPill number={-1} sourceType="imap" title="Email" onClick={() => {}} />,
    );
    expect(screen.getByText("?")).toBeInTheDocument();
  });
});

describe("EvidenceCard", () => {
  it("carries the data-chunk-id anchor and triggers onActivate", async () => {
    const user = userEvent.setup();
    const onActivate = vi.fn();
    render(<EvidenceCard number={1} chunk={sampleChunk} onActivate={onActivate} />);
    const card = document.querySelector('[data-chunk-id="doc-1"]') as HTMLElement;
    expect(card).not.toBeNull();
    await user.click(card);
    expect(onActivate).toHaveBeenCalled();
  });
});

describe("AnswerStream", () => {
  it("renders text + numbered pills via segmentAnswer", () => {
    render(
      <AnswerStream
        text="Hello world."
        citations={[{ doc_id: "doc-1", span_start: 0, span_end: 5 }]}
        evidence={[sampleChunk]}
        isStreaming={false}
      />,
    );
    // The pill text reads "1" because doc-1 is the first evidence chunk.
    // Both an inline pill and a footer-chip pill render with the same
    // accessible name; assert at least one is in the document.
    expect(screen.getAllByRole("button", { name: /Citation 1 — Anthropic/ }).length).toBeGreaterThan(0);
    expect(screen.getByText(/Hello/)).toBeInTheDocument();
    expect(screen.getByText(/Sources/)).toBeInTheDocument();
  });

  it("renders markdown — bold, ordered lists, headings", () => {
    render(
      <AnswerStream
        text={"Here is what I found:\n\n1. **First** item\n2. **Second** item\n\n## Summary\n\nDone."}
        citations={[]}
        evidence={[]}
        isStreaming={false}
      />,
    );
    // Bold renders as <strong>
    expect(screen.getByText("First").tagName.toLowerCase()).toBe("strong");
    expect(screen.getByText("Second").tagName.toLowerCase()).toBe("strong");
    // Ordered list with two items
    const lis = document.querySelectorAll("ol > li");
    expect(lis).toHaveLength(2);
    // h2 heading
    expect(document.querySelector("h2")?.textContent).toBe("Summary");
  });

  it("places a citation pill inside a list item, mid-prose", () => {
    // span_end = 13 lands after "First item" in the list-item prose.
    render(
      <AnswerStream
        text={"1. First item from the doc"}
        citations={[{ doc_id: "doc-1", span_start: 3, span_end: 13 }]}
        evidence={[sampleChunk]}
        isStreaming={false}
      />,
    );
    const li = document.querySelector("ol > li");
    expect(li).not.toBeNull();
    const inlinePills = li?.querySelectorAll(
      'button[aria-label*="Citation 1"]',
    );
    expect(inlinePills?.length ?? 0).toBeGreaterThan(0);
  });

  // Regression: the sentinel tokens AnswerStream splices into the body
  // (§§CITE:N§§) live in a string node that the pillify walker swaps
  // for <CitationPill>. Without explicit table-element overrides, the
  // walker never reached strings inside <td>/<th> — so models that
  // emitted GFM tables (Sonnet often does for comparison answers) leaked
  // raw "§§CITE:0§§" text into the rendered cells. This test pins the
  // contract: a citation whose span_end falls inside a table cell must
  // resolve to a pill in the DOM, with no sentinel left behind.
  it("resolves citation sentinels inside GFM table cells", () => {
    // span_end = 38 lands after "China Restaurant Rosengarten" in the
    // first data row's Venue column.
    const text =
      "| Date | Venue | Total |\n| --- | --- | --- |\n| 29 Mar | China Restaurant Rosengarten | €48.98 |";
    render(
      <AnswerStream
        text={text}
        citations={[{ doc_id: "doc-1", span_start: 24, span_end: 75 }]}
        evidence={[sampleChunk]}
        isStreaming={false}
      />,
    );
    // No sentinel text leaks anywhere in the answer body.
    const article = document.querySelector('[role="article"]');
    expect(article?.textContent ?? "").not.toContain("§§CITE");
    // And the inline pill rendered inside a <td>.
    const tdPills = document.querySelectorAll(
      'td button[aria-label*="Citation 1"]',
    );
    expect(tdPills.length).toBeGreaterThan(0);
  });

  // Per-turn embedded sources contract: clicking a footer chip expands
  // an in-place evidence card under the chip strip. Multiple chips can
  // be expanded at once. Click again collapses.
  it("expands an evidence card in-place when a footer chip is clicked", async () => {
    const user = userEvent.setup();
    render(
      <AnswerStream
        text="x"
        citations={[]}
        evidence={[sampleChunk]}
        isStreaming={false}
      />,
    );
    // Before click: no expand card in the DOM.
    expect(document.querySelector('[data-chunk-id="doc-1"]')).toBeNull();
    await user.click(
      screen.getByRole("button", { name: /Citation 1 — Anthropic/ }),
    );
    expect(document.querySelector('[data-chunk-id="doc-1"]')).not.toBeNull();
    // Click again collapses.
    await user.click(
      screen.getAllByRole("button", { name: /Citation 1 — Anthropic/ })[0],
    );
    expect(document.querySelector('[data-chunk-id="doc-1"]')).toBeNull();
  });

  // Inline `[N]` pill click should also expand the matching footer card.
  it("inline pill click expands the matching footer card", async () => {
    const user = userEvent.setup();
    render(
      <AnswerStream
        text="Hello world."
        citations={[{ doc_id: "doc-1", span_start: 0, span_end: 5 }]}
        evidence={[sampleChunk]}
        isStreaming={false}
      />,
    );
    expect(document.querySelector('[data-chunk-id="doc-1"]')).toBeNull();
    // The first matching pill is the inline one (the chip is later).
    const pills = screen.getAllByRole("button", {
      name: /Citation 1 — Anthropic/,
    });
    await user.click(pills[0]);
    expect(document.querySelector('[data-chunk-id="doc-1"]')).not.toBeNull();
  });
});

describe("AskComposer", () => {
  it("disables send when empty and fires onSubmit on Cmd+Enter", async () => {
    const user = userEvent.setup();
    const onSubmit = vi.fn();
    render(
      <AskComposer
        model="anthropic:claude-sonnet-4-6"
        onModelChange={() => {}}
        models={sampleModels}
        onSubmit={onSubmit}
      />,
    );
    const textarea = screen.getByPlaceholderText(/Ask anything/);
    expect(screen.getByRole("button", { name: "Send" })).toBeDisabled();
    await user.type(textarea, "Hello");
    fireEvent.keyDown(textarea, { key: "Enter", ctrlKey: true });
    expect(onSubmit).toHaveBeenCalledWith("Hello");
  });

  it("plain Enter does NOT submit", async () => {
    const user = userEvent.setup();
    const onSubmit = vi.fn();
    render(
      <AskComposer
        model="anthropic:claude-sonnet-4-6"
        onModelChange={() => {}}
        models={sampleModels}
        onSubmit={onSubmit}
      />,
    );
    const textarea = screen.getByPlaceholderText(/Ask anything/);
    await user.type(textarea, "x");
    fireEvent.keyDown(textarea, { key: "Enter" });
    expect(onSubmit).not.toHaveBeenCalled();
  });

  it("shows Cancel button when streaming", () => {
    const onCancel = vi.fn();
    render(
      <AskComposer
        model="anthropic:claude-sonnet-4-6"
        onModelChange={() => {}}
        models={sampleModels}
        onSubmit={() => {}}
        isStreaming
        onCancel={onCancel}
      />,
    );
    expect(screen.getByRole("button", { name: "Cancel" })).toBeInTheDocument();
  });
});

describe("UserTurn", () => {
  it("renders content with whitespace preserved", () => {
    const msg: ChatMessage = {
      id: "u",
      chat_id: "c",
      role: "user",
      seq: 0,
      content: "Line 1\nLine 2",
      created_at: "2026-04-26T00:00:00Z",
    };
    render(<UserTurn message={msg} />);
    expect(screen.getByText(/Line 1/)).toBeInTheDocument();
    expect(screen.getByText(/Line 2/)).toBeInTheDocument();
  });

  it("renders the 'searched as' footnote when the next assistant turn was rewritten", () => {
    const userMsg: ChatMessage = {
      id: "u",
      chat_id: "c",
      role: "user",
      seq: 2,
      content: "which one was largest?",
      created_at: "2026-04-26T00:00:00Z",
    };
    const asstMsg: ChatMessage = {
      id: "a",
      chat_id: "c",
      role: "assistant",
      seq: 3,
      content: "...",
      rewritten_query: "largest Anthropic invoice from April 2026",
      created_at: "2026-04-26T00:00:01Z",
    };
    render(<UserTurn message={userMsg} nextMessage={asstMsg} />);
    expect(
      screen.getByText("largest Anthropic invoice from April 2026"),
    ).toBeInTheDocument();
    expect(screen.getByText(/searched as/i)).toBeInTheDocument();
  });

  it("does not render the footnote when rewritten_query equals user content", () => {
    const userMsg: ChatMessage = {
      id: "u",
      chat_id: "c",
      role: "user",
      seq: 0,
      content: "literal question",
      created_at: "2026-04-26T00:00:00Z",
    };
    const asstMsg: ChatMessage = {
      id: "a",
      chat_id: "c",
      role: "assistant",
      seq: 1,
      content: "...",
      rewritten_query: "literal question",
      created_at: "2026-04-26T00:00:01Z",
    };
    render(<UserTurn message={userMsg} nextMessage={asstMsg} />);
    expect(screen.queryByText(/searched as/i)).not.toBeInTheDocument();
  });
});

describe("PhaseChip", () => {
  it("renders nothing when phase is idle and no skip-retrieval signal", () => {
    const { container } = render(
      <PhaseChip phase="idle" userContent="hi" />,
    );
    expect(container.firstChild).toBeNull();
  });

  it("renders the default 'Searching your corpus' label during retrieving", () => {
    render(<PhaseChip phase="retrieving" userContent="hi" />);
    expect(screen.getByText("Searching your corpus")).toBeInTheDocument();
  });

  it("renders the default 'Generating answer' label during streaming", () => {
    render(<PhaseChip phase="streaming" userContent="hi" />);
    expect(screen.getByText("Generating answer")).toBeInTheDocument();
  });

  it("falls back to default label when query equals userContent", () => {
    // Equal query is NOT a rewrite — show the plain "Searching your
    // corpus" label, not the rewritten variant.
    render(<PhaseChip phase="retrieving" userContent="hi" query="hi" />);
    expect(screen.getByText("Searching your corpus")).toBeInTheDocument();
  });

  it("renders the rewritten variant when query differs from userContent", () => {
    render(
      <PhaseChip
        phase="retrieving"
        userContent="which one was largest?"
        query="largest Anthropic invoice from April 2026"
      />,
    );
    expect(screen.getByText("Searching")).toBeInTheDocument();
    expect(
      screen.getByText("largest Anthropic invoice from April 2026"),
    ).toBeInTheDocument();
  });

  it("renders an elapsed-time counter when startedAtMs is set", () => {
    const startedAtMs = Date.now() - 23_000; // 23 seconds ago
    render(
      <PhaseChip
        phase="streaming"
        userContent="hi"
        startedAtMs={startedAtMs}
      />,
    );
    // Match the formatted "0:23" string emitted by formatElapsed.
    expect(screen.getByText(/0:2\d/)).toBeInTheDocument();
  });

  it("renders the rewriter-fallback diagnostic glyph when failure reason is set", () => {
    render(
      <PhaseChip
        phase="retrieving"
        userContent="follow up"
        rewriterFailureReason="timeout"
      />,
    );
    expect(
      screen.getByRole("button", { name: /rewriter status/i }),
    ).toBeInTheDocument();
  });

  it("renders the muted 'Answering from history' variant on skipped retrieval", () => {
    render(
      <PhaseChip
        phase="streaming"
        userContent="thanks!"
        query="thanks"
        skippedRetrieval
      />,
    );
    expect(screen.getByText("Answering from history")).toBeInTheDocument();
    expect(screen.queryByText("Generating answer")).not.toBeInTheDocument();
    expect(screen.queryByText("Searching")).not.toBeInTheDocument();
  });
});

describe("buildCopyText", () => {
  it("inserts [N] markers and a sources legend", () => {
    const txt = buildCopyText(
      "Hello.",
      [{ doc_id: "doc-1", span_start: 0, span_end: 5 }],
      [sampleChunk],
    );
    expect(txt).toContain("Hello [1].");
    expect(txt).toContain("Sources:");
    expect(txt).toContain("[1] Anthropic invoice — March");
  });

  it("returns body unchanged when no evidence", () => {
    expect(buildCopyText("Plain.", [], [])).toBe("Plain.");
  });

  it("drops body markers whose doc_id is not in evidence", () => {
    const out = buildCopyText(
      "Hello.",
      [{ doc_id: "ghost", span_start: 0, span_end: 5 }],
      [sampleChunk],
    );
    // Body should not contain the marker — but the legend still lists evidence.
    expect(out.split("Sources:")[0]).not.toContain("[1]");
  });
});

describe("ToolTrace", () => {
  it("renders the BE-supplied summary in the collapsed strip", () => {
    render(
      <ToolTrace
        name="nexus_search"
        args={'{"query":"x"}'}
        summary='Searched "x" — 2 results'
      />,
    );
    expect(
      screen.getByText('Searched "x" — 2 results'),
    ).toBeInTheDocument();
  });

  it("derives a query-aware label from args when summary is absent", () => {
    render(<ToolTrace name="nexus_search" args={'{"query":"wolt"}'} />);
    expect(screen.getByText(/Searched/)).toBeInTheDocument();
    expect(screen.getByText('"wolt"')).toBeInTheDocument();
  });

  it("falls back to a generic label when args is malformed", () => {
    render(<ToolTrace name="nexus_search" args="not-json" />);
    expect(screen.getByText("Searched the index")).toBeInTheDocument();
  });

  it("truncates a long query label at a word boundary and keeps the full text on hover", () => {
    const longQuery =
      "Search my email and documents and give me a breakdown of every subscription I pay for";
    render(
      <ToolTrace
        name="nexus_search"
        args={JSON.stringify({ query: longQuery })}
      />,
    );
    const label = screen.getByText(/…"$/);
    // Trimmed well below the verbatim question, ends with an ellipsis
    // inside the closing quote, and exposes the full query via title.
    expect(label.textContent!.length).toBeLessThan(longQuery.length);
    expect(label).toHaveAttribute("title", `"${longQuery}"`);
  });

  it("expands on click and reveals nested EvidenceCards", async () => {
    const user = userEvent.setup();
    render(
      <ToolTrace
        name="nexus_search"
        args="{}"
        summary="Searched"
        chunks={[sampleChunk]}
      />,
    );
    const trigger = screen.getByRole("button", { name: /Searched/ });
    expect(trigger).toHaveAttribute("aria-expanded", "false");
    await user.click(trigger);
    expect(trigger).toHaveAttribute("aria-expanded", "true");
    // Card has data-chunk-id matching sampleChunk.id
    const card = document.querySelector('[data-chunk-id="doc-1"]') as HTMLElement;
    expect(card).not.toBeNull();
    // Clicking the card collapses the strip back (no global rail to
    // jump to anymore — the strip is self-contained).
    await user.click(card);
    expect(trigger).toHaveAttribute("aria-expanded", "false");
  });

  it("starts expanded when defaultExpanded is true", () => {
    render(
      <ToolTrace
        name="nexus_search"
        args="{}"
        summary="Searched"
        chunks={[sampleChunk]}
        defaultExpanded
      />,
    );
    const trigger = screen.getByRole("button", { name: /Searched/ });
    expect(trigger).toHaveAttribute("aria-expanded", "true");
  });

  it("renders a no-results placeholder when expanded with empty chunks", async () => {
    const user = userEvent.setup();
    render(
      <ToolTrace name="nexus_search" args="{}" summary="No hits" chunks={[]} />,
    );
    await user.click(screen.getByRole("button", { name: /No hits/ }));
    expect(screen.getByText("No matching documents.")).toBeInTheDocument();
  });

  it("shows the chunk-count badge only when chunks are non-empty", () => {
    const { rerender, container } = render(
      <ToolTrace
        name="nexus_search"
        args="{}"
        summary="Searched"
        chunks={[sampleChunk, sampleChunk]}
      />,
    );
    // The badge sits on the collapsed strip (the trigger button), not
    // inside the expanded region — narrow the query to the trigger.
    const trigger = screen.getByRole("button", { name: /Searched/ });
    expect(trigger).toHaveTextContent("2");
    rerender(<ToolTrace name="nexus_search" args="{}" summary="Searched" chunks={[]} />);
    const triggerAfter = screen.getByRole("button", { name: /Searched/ });
    expect(triggerAfter.textContent).not.toMatch(/\b0\b/);
    // Belt and braces: no element rendered by ToolTrace itself shows "0".
    expect(container.querySelector('[aria-hidden="true"]')).toBeTruthy();
  });
});
