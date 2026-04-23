import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";

import { SyncProgress } from "../sync-progress-bar";

describe("SyncProgress", () => {
  it("renders 'Discovering…' when total is 0", () => {
    render(<SyncProgress processed={0} total={0} />);
    expect(screen.getByText("Discovering…")).toBeInTheDocument();
  });

  it("renders the counter when a total is known", () => {
    render(<SyncProgress processed={25} total={100} />);
    // The animated number starts at the target on first render, so
    // 25/100 shows up without waiting for a tween.
    expect(screen.getByText("25")).toBeInTheDocument();
    expect(screen.getByText(/100/)).toBeInTheDocument();
  });

  it("renders the scope label alongside the counter", () => {
    // IMAP emits a folder name; Telegram emits a chat name.
    // Trimmed non-empty scope must appear before the counter.
    render(<SyncProgress processed={5} total={50} scope="Archive" />);
    expect(screen.getByText("Archive")).toBeInTheDocument();
    expect(screen.getByText("5")).toBeInTheDocument();
  });

  it("hides the scope label when it's just whitespace", () => {
    render(<SyncProgress processed={1} total={2} scope="   " />);
    // Whitespace-only scope should NOT leak into the DOM as an
    // otherwise-blank span — a trimmed empty string means "no
    // scope yet".
    expect(screen.queryByText(/^\s+$/)).not.toBeInTheDocument();
  });

  it("shows the error count when errors > 0", () => {
    render(<SyncProgress processed={10} total={100} errors={3} />);
    expect(screen.getByText("3 errors")).toBeInTheDocument();
  });

  it("uses the singular form for a single error", () => {
    render(<SyncProgress processed={10} total={100} errors={1} />);
    expect(screen.getByText("1 error")).toBeInTheDocument();
  });

  it("omits the label row entirely in compact mode", () => {
    const { container } = render(
      <SyncProgress processed={5} total={10} compact />,
    );
    expect(container.textContent).toBe("");
  });

  it("clamps the progress percentage at 100", () => {
    // Defensive: if processed somehow exceeds total (e.g. a late
    // frame arrives after an estimate-total reduction), the bar
    // must still render a sane width.
    render(<SyncProgress processed={500} total={10} />);
    const bar = screen.getByRole("progressbar");
    expect(bar.getAttribute("aria-valuenow")).toBe("100");
  });
});
