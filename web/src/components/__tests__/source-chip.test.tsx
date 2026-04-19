import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import { SourceChip, sourceMetaFor } from "../source-chip";

describe("SourceChip", () => {
  it("default variant renders the source label and icon", () => {
    render(<SourceChip type="imap" />);
    // imap → "Email" per SOURCE_META
    expect(screen.getByText("Email")).toBeInTheDocument();
  });

  it("renders a compact icon-only chip with aria-label for screen readers", () => {
    const { container } = render(
      <SourceChip type="telegram" variant="compact" />,
    );
    expect(container.querySelector('[aria-label="Telegram"]')).not.toBeNull();
    expect(screen.queryByText("Telegram")).not.toBeInTheDocument();
  });

  it("pill variant renders label and trailing count", () => {
    render(
      <SourceChip type="paperless" variant="pill" count={42} />,
    );
    expect(screen.getByText("Paperless")).toBeInTheDocument();
    expect(screen.getByText("42")).toBeInTheDocument();
  });

  it("uses the provided label override on default variant", () => {
    render(<SourceChip type="imap" label="Email · personal" />);
    expect(screen.getByText("Email · personal")).toBeInTheDocument();
  });

  it("falls back to the raw type for unknown sources", () => {
    const meta = sourceMetaFor("unknown-source-type");
    expect(meta.label).toBe("unknown-source-type");
  });
});
