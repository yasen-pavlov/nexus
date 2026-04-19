import { describe, expect, it } from "vitest";
import { siTelegram, siPaperlessngx } from "simple-icons";

import { ConnectorLogo, connectorTypeLabel } from "../connector-logo";
import { render } from "@/test/test-utils";

describe("ConnectorLogo", () => {
  it.each([
    ["telegram", siTelegram.title, siTelegram.path],
    ["paperless", siPaperlessngx.title, siPaperlessngx.path],
  ])("renders the simple-icons brand SVG for %s", (type, brandTitle, brandPath) => {
    const { container } = render(<ConnectorLogo type={type} size="md" />);
    // Plate sets aria-hidden, so role=img queries are muted; fall back
    // to asserting the brand's path string is in the DOM (+ its label).
    const svg = container.querySelector(`svg[aria-label="${brandTitle}"]`);
    expect(svg).toBeTruthy();
    const path = svg?.querySelector("path");
    expect(path?.getAttribute("d")).toBe(brandPath);
  });

  it.each(["imap", "filesystem", "unknown-type"])(
    "renders a lucide fallback for %s (no brand asset)",
    (type) => {
      render(<ConnectorLogo type={type} />);
      // Lucide icons render as <svg> without role=img + name, so we assert
      // the logo plate is mounted by querying for its parent div — the
      // plate is the only element with `aria-hidden`.
      expect(document.querySelector("[aria-hidden='true']")).toBeTruthy();
    },
  );

  it("honors the size prop via inline width/height", () => {
    const { container } = render(<ConnectorLogo type="filesystem" size="lg" />);
    const plate = container.firstChild as HTMLElement;
    // size=lg → 56px per the SIZE map.
    expect(plate.style.width).toBe("56px");
    expect(plate.style.height).toBe("56px");
  });
});

describe("connectorTypeLabel", () => {
  it("gives each known type a human-readable label", () => {
    expect(connectorTypeLabel("filesystem")).toBe("Filesystem");
    expect(connectorTypeLabel("imap")).toBe("Email · IMAP");
    expect(connectorTypeLabel("paperless")).toBe("Paperless-ngx");
    expect(connectorTypeLabel("telegram")).toBe("Telegram");
  });

  it("returns the raw type as the label for unknown sources", () => {
    expect(connectorTypeLabel("confluence")).toBe("confluence");
  });
});
