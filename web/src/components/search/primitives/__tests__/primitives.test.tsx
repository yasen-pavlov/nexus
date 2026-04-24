import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import { Clock } from "lucide-react";
import type { DocumentHit } from "@/lib/api-types";
import { FileTypeIcon } from "../file-type-icon";
import { InitialsAvatar } from "../initials-avatar";
import { TagPill } from "../tag-pill";
import { MetaRow } from "../meta-row";
import { SnippetBody } from "../snippet-body";

function baseHit(overrides: Partial<DocumentHit> = {}): DocumentHit {
  return {
    id: "d1",
    source_type: "imap",
    source_name: "mail",
    source_id: "sid",
    title: "t",
    content: "",
    visibility: "private",
    created_at: "2026-04-17T00:00:00Z",
    indexed_at: "2026-04-17T00:00:00Z",
    rank: 1,
    ...overrides,
  };
}

describe("FileTypeIcon", () => {
  it("falls back to generic file icon when nothing matches", () => {
    const { container } = render(<FileTypeIcon className="test-icon" />);
    const svg = container.querySelector("svg");
    expect(svg).not.toBeNull();
    expect(svg?.getAttribute("class") ?? "").toContain("test-icon");
  });

  it("renders without crashing for a known mime type", () => {
    const { container } = render(<FileTypeIcon mime="application/pdf" />);
    expect(container.querySelector("svg")).not.toBeNull();
  });

  it("uses extension when mime is octet-stream", () => {
    const { container } = render(
      <FileTypeIcon mime="application/octet-stream" extension=".pdf" />,
    );
    expect(container.querySelector("svg")).not.toBeNull();
  });

  it("handles archive, image, audio, video, code, spreadsheet, presentation families", () => {
    const mimes = [
      "image/png",
      "audio/mpeg",
      "video/mp4",
      "application/zip",
      "application/json",
      "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
      "application/vnd.openxmlformats-officedocument.presentationml.presentation",
      "application/msword",
      "text/plain",
      "text/csv",
      "text/rtf",
    ];
    for (const mime of mimes) {
      const { container, unmount } = render(<FileTypeIcon mime={mime} />);
      expect(container.querySelector("svg")).not.toBeNull();
      unmount();
    }
  });

  it("handles unknown extensions gracefully", () => {
    const { container } = render(<FileTypeIcon extension=".xyz" />);
    expect(container.querySelector("svg")).not.toBeNull();
  });
});

describe("InitialsAvatar", () => {
  it("renders initials derived from name", () => {
    render(<InitialsAvatar name="Alice Smith" seed="alice@example.com" />);
    expect(screen.getByText("AS")).toBeInTheDocument();
  });

  it("falls back to seed when no name given", () => {
    render(<InitialsAvatar seed="bob@example.com" />);
    // initialsFor uses first 2 chars of the seed (spaces stripped)
    expect(screen.getByText("BO")).toBeInTheDocument();
  });

  it("exposes name via aria-label", () => {
    render(<InitialsAvatar name="Carol" seed="c" />);
    expect(screen.getByRole("img")).toHaveAttribute("aria-label", "Carol");
  });
});

describe("TagPill", () => {
  it("renders the label", () => {
    render(<TagPill label="health" />);
    expect(screen.getByText("health")).toBeInTheDocument();
  });

  it("uses label as default title", () => {
    render(<TagPill label="health" />);
    expect(screen.getByText("health")).toHaveAttribute("title", "health");
  });

  it("honors explicit title", () => {
    render(<TagPill label="2026" title="year" />);
    expect(screen.getByText("2026")).toHaveAttribute("title", "year");
  });
});

describe("MetaRow", () => {
  it("renders visible items with separator", () => {
    render(
      <MetaRow
        items={[
          { key: "date", label: "Apr 17" },
          { key: "size", label: "2 KB", numeric: true },
        ]}
      />,
    );
    expect(screen.getByText("Apr 17")).toBeInTheDocument();
    expect(screen.getByText("2 KB")).toBeInTheDocument();
    expect(screen.getByText("·")).toBeInTheDocument();
  });

  it("returns null when no items are visible", () => {
    const { container } = render(<MetaRow items={[false, null, undefined]} />);
    expect(container.firstChild).toBeNull();
  });

  it("renders icon when provided", () => {
    const { container } = render(
      <MetaRow items={[{ key: "time", icon: Clock, label: "now" }]} />,
    );
    expect(container.querySelector("svg")).not.toBeNull();
  });

  it("applies tabular-nums to numeric items", () => {
    render(
      <MetaRow
        items={[{ key: "n", label: "1234", numeric: true }]}
      />,
    );
    expect(screen.getByText("1234").className).toContain("tabular-nums");
  });
});

describe("SnippetBody", () => {
  it("renders headline HTML when present", () => {
    const { container } = render(
      <SnippetBody hit={baseHit({ headline: "hello <em>world</em>" })} />,
    );
    expect(container.querySelector("em")?.textContent).toBe("world");
  });

  it("falls back to content when headline is empty", () => {
    render(<SnippetBody hit={baseHit({ content: "plain body" })} />);
    expect(screen.getByText("plain body")).toBeInTheDocument();
  });

  it("returns null when neither headline nor content", () => {
    const { container } = render(<SnippetBody hit={baseHit()} />);
    expect(container.firstChild).toBeNull();
  });

  it("applies chosen line-clamp", () => {
    const { container } = render(
      <SnippetBody hit={baseHit({ content: "abc" })} lineClamp={3} />,
    );
    expect(container.querySelector("p")?.className).toContain("line-clamp-3");
  });
});
