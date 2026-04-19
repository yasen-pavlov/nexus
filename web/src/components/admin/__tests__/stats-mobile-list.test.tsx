import { describe, it, expect } from "vitest";
import { renderWithRouter, screen } from "@/test/test-utils";

import { StatsMobileList } from "../stats-mobile-list";
import type { AdminPerSourceStats } from "@/lib/api-types";

const rows: AdminPerSourceStats[] = [
  {
    source_type: "imap",
    source_name: "personal-email",
    document_count: 1234,
    chunk_count: 5678,
    cache_bytes: 1024 * 1024 * 50,
    cache_count: 10,
    latest_indexed_at: "2026-04-10T10:00:00Z",
  },
  {
    source_type: "telegram",
    source_name: "my-telegram",
    document_count: 42,
    chunk_count: 84,
    cache_bytes: 0,
    cache_count: 0,
    latest_indexed_at: undefined,
  },
];

describe("StatsMobileList", () => {
  it("renders one card per source with name + document count", async () => {
    renderWithRouter(<StatsMobileList rows={rows} />, {
      extraRoutes: ["/connectors"],
    });
    expect(await screen.findByText("personal-email")).toBeInTheDocument();
    expect(screen.getByText("my-telegram")).toBeInTheDocument();
    expect(screen.getByText("1,234")).toBeInTheDocument();
  });

  it("shows the per-source open link", async () => {
    renderWithRouter(<StatsMobileList rows={rows} />, {
      extraRoutes: ["/connectors"],
    });
    await screen.findByText("personal-email");
    const links = screen.getAllByRole("link", {
      name: /view connectors filtered by/i,
    });
    expect(links.length).toBe(rows.length);
  });
});
