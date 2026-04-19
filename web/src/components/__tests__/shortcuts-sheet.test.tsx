import { describe, it, expect } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";

import { ShortcutsSheet } from "../shortcuts-sheet";

describe("ShortcutsSheet", () => {
  it("lists every section and the chord tip when open", async () => {
    render(<ShortcutsSheet open onOpenChange={() => {}} />);

    await waitFor(() => {
      expect(screen.getByText(/keyboard shortcuts/i)).toBeInTheDocument();
    });

    expect(screen.getByText("Navigation")).toBeInTheDocument();
    expect(screen.getByText("Search")).toBeInTheDocument();
    expect(screen.getByText("Application")).toBeInTheDocument();

    expect(screen.getByText(/go to search/i)).toBeInTheDocument();
    expect(screen.getByText(/go to connectors/i)).toBeInTheDocument();
    expect(screen.getByText(/go to admin settings/i)).toBeInTheDocument();
    expect(screen.getByText(/focus search input/i)).toBeInTheDocument();
    expect(screen.getByText(/command palette/i)).toBeInTheDocument();
    expect(screen.getByText(/toggle sidebar/i)).toBeInTheDocument();
    expect(screen.getByText(/dismiss any overlay/i)).toBeInTheDocument();

    expect(screen.getByText(/200/)).toBeInTheDocument(); // 200 ms tip
  });

  it("renders a close button so the sheet is dismissible without keyboard", async () => {
    render(<ShortcutsSheet open onOpenChange={() => {}} />);
    await waitFor(() => {
      expect(screen.getByText(/keyboard shortcuts/i)).toBeInTheDocument();
    });
    expect(
      screen.getByRole("button", { name: /close/i }),
    ).toBeInTheDocument();
  });

  it("renders nothing when open is false", () => {
    render(<ShortcutsSheet open={false} onOpenChange={() => {}} />);
    expect(screen.queryByText(/keyboard shortcuts/i)).not.toBeInTheDocument();
  });
});
