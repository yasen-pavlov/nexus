import { describe, it, expect } from "vitest";
import { renderWithRouter, screen, userEvent, waitFor } from "@/test/test-utils";
import type { Facet } from "@/lib/api-types";
import { SearchFilters } from "../search-filters";

const basicFacets: Record<string, Facet[]> = {
  source_type: [
    { value: "imap", count: 7 },
    { value: "telegram", count: 3 },
  ],
  source_name: [
    { value: "personal-email", count: 7 },
    { value: "tg-main", count: 3 },
  ],
};

describe("SearchFilters", () => {
  it("renders a colored pill per source-type facet with counts", async () => {
    renderWithRouter(
      <SearchFilters params={{ q: "test" }} facets={basicFacets} />,
    );
    // Labels use sourceMetaFor: imap → Email, telegram → Telegram.
    await waitFor(() => {
      expect(screen.getByText("Email")).toBeInTheDocument();
    });
    expect(screen.getByText("Telegram")).toBeInTheDocument();
    expect(screen.getByText("7")).toBeInTheDocument();
    expect(screen.getByText("3")).toBeInTheDocument();
  });

  it("shows Clear when a filter is active and hides it otherwise", async () => {
    const { rerender } = renderWithRouter(
      <SearchFilters params={{ q: "test" }} facets={basicFacets} />,
    );
    await waitFor(() => {
      expect(screen.getByText("Email")).toBeInTheDocument();
    });
    expect(screen.queryByRole("button", { name: "Clear" })).not.toBeInTheDocument();

    rerender(
      <SearchFilters
        params={{ q: "test", sources: ["imap"] }}
        facets={basicFacets}
      />,
    );
    expect(screen.getByRole("button", { name: "Clear" })).toBeInTheDocument();
  });

  it("returns null when there are no facets and no active filters", () => {
    const { container } = renderWithRouter(
      <SearchFilters params={{ q: "test" }} facets={{}} />,
    );
    // Only the router/tanstack scaffolding renders — no filter chrome.
    expect(container.querySelector('button[aria-pressed]')).toBeNull();
    expect(screen.queryByText(/any time/i)).not.toBeInTheDocument();
  });

  it("renders the date button with 'Any time' default and opens preset popover on click", async () => {
    renderWithRouter(
      <SearchFilters params={{ q: "test" }} facets={basicFacets} />,
    );
    const user = userEvent.setup();
    await waitFor(() => {
      expect(screen.getByText("Any time")).toBeInTheDocument();
    });

    await user.click(screen.getByRole("button", { name: /any time/i }));

    // Popover opens — check section label + a few presets
    expect(screen.getByText("Date range")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Past 7 days" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Past 30 days" })).toBeInTheDocument();
    expect(screen.getByText("Custom range")).toBeInTheDocument();
  });

  it("marks an active source pill with aria-pressed", async () => {
    renderWithRouter(
      <SearchFilters
        params={{ q: "test", sources: ["imap"] }}
        facets={basicFacets}
      />,
    );
    await waitFor(() => {
      // Two buttons exist (imap active, telegram not active)
      const emailBtn = screen.getByRole("button", {
        pressed: true,
        name: /Email/,
      });
      expect(emailBtn).toBeInTheDocument();
    });
    expect(
      screen.getByRole("button", { pressed: false, name: /Telegram/ }),
    ).toBeInTheDocument();
  });
});
