import { describe, expect, it, vi } from "vitest";
import { fireEvent } from "@testing-library/react";

import { render, screen, userEvent } from "@/test/test-utils";
import type { ChatMessage, ChunkPreview, LLMModelInfo } from "@/lib/api-types";

import { AnswerStream } from "../answer-stream";
import { AskComposer } from "../ask-composer";
import { CitationPill } from "../citation-pill";
import { EvidenceCard } from "../evidence-card";
import { EvidenceRail } from "../evidence-rail";
import { ExamplePill } from "../example-pill";
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

describe("EvidenceRail", () => {
  it("renders the empty state when chunks is empty", () => {
    render(<EvidenceRail chunks={[]} onActivate={() => {}} />);
    expect(screen.getByText(/Run a question to see what backed it/)).toBeInTheDocument();
  });

  it("renders one card per chunk", () => {
    render(
      <EvidenceRail
        chunks={[sampleChunk, { ...sampleChunk, id: "doc-2", title: "Doc 2" }]}
        onActivate={() => {}}
      />,
    );
    expect(document.querySelector('[data-chunk-id="doc-1"]')).not.toBeNull();
    expect(document.querySelector('[data-chunk-id="doc-2"]')).not.toBeNull();
  });
});

describe("AnswerStream", () => {
  it("renders text + numbered pills via segmentAnswer", () => {
    const onJump = vi.fn();
    render(
      <AnswerStream
        text="Hello world."
        citations={[{ doc_id: "doc-1", span_start: 0, span_end: 5 }]}
        evidence={[sampleChunk]}
        isStreaming={false}
        onJumpToEvidence={onJump}
      />,
    );
    // The pill text reads "1" because doc-1 is the first evidence chunk.
    // Both an inline pill and a sources-rail pill render with the same
    // accessible name; assert at least one is in the document.
    expect(screen.getAllByRole("button", { name: /Citation 1 — Anthropic/ }).length).toBeGreaterThan(0);
    expect(screen.getByText(/Hello/)).toBeInTheDocument();
    expect(screen.getByText(/Sources/)).toBeInTheDocument();
  });

  it("calls onJumpToEvidence when a sources-rail pill is clicked", async () => {
    const user = userEvent.setup();
    const onJump = vi.fn();
    render(
      <AnswerStream
        text="x"
        citations={[]}
        evidence={[sampleChunk]}
        isStreaming={false}
        onJumpToEvidence={onJump}
      />,
    );
    await user.click(screen.getByRole("button", { name: /Citation 1 — Anthropic/ }));
    expect(onJump).toHaveBeenCalledWith("doc-1");
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
