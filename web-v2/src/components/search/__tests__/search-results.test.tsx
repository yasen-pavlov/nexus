import { describe, it, expect } from "vitest";
import { http, HttpResponse } from "msw";
import { renderWithRouter, screen, userEvent, waitFor } from "@/test/test-utils";
import { server } from "@/test/mocks/server";
import { setToken } from "@/lib/api-client";
import { fakeToken, sampleHits } from "@/test/mocks/handlers";
import { SearchResults } from "../search-results";

describe("SearchResults", () => {
  it("shows the welcome state when query is empty", async () => {
    setToken(fakeToken);
    renderWithRouter(<SearchResults params={{}} />);
    await waitFor(() => {
      expect(screen.getByText("ready.")).toBeInTheDocument();
    });
  });

  it("renders result cards per source_type", async () => {
    setToken(fakeToken);
    renderWithRouter(<SearchResults params={{ q: "test" }} />);

    await waitFor(() => {
      expect(screen.getByText("Subject of an email")).toBeInTheDocument();
    });
    expect(screen.getByText("Chat window")).toBeInTheDocument();
    expect(screen.getByText("Hospital invoice")).toBeInTheDocument();
    // meeting.md appears both as title and inside the filesystem path body
    expect(screen.getAllByText("meeting.md").length).toBeGreaterThan(0);

    // Telegram card has the channel "#" accent
    expect(screen.getByText("Family")).toBeInTheDocument();
  });

  it("shows the no-results state when the query returns zero hits", async () => {
    setToken(fakeToken);
    server.use(
      http.get("*/api/search", () =>
        HttpResponse.json({
          data: {
            documents: [],
            total_count: 0,
            query: "zzz",
            facets: {},
          },
        }),
      ),
    );
    renderWithRouter(<SearchResults params={{ q: "zzz" }} />);
    await waitFor(() => {
      expect(screen.getByText(/Nothing matched/)).toBeInTheDocument();
    });
    expect(screen.getByText("zzz")).toBeInTheDocument();
  });

  it("shows the load-more button when there are more hits", async () => {
    setToken(fakeToken);
    server.use(
      http.get("*/api/search", ({ request }) => {
        const url = new URL(request.url);
        const offset = Number(url.searchParams.get("offset") ?? 0);
        return HttpResponse.json({
          data: {
            documents: offset === 0 ? sampleHits.slice(0, 2) : sampleHits.slice(2),
            total_count: sampleHits.length,
            query: "test",
            facets: {},
          },
        });
      }),
    );

    renderWithRouter(<SearchResults params={{ q: "test" }} />);
    await waitFor(() => {
      expect(screen.getByText("Subject of an email")).toBeInTheDocument();
    });

    const loadMore = screen.getByRole("button", { name: /load more/i });
    await userEvent.setup().click(loadMore);

    await waitFor(() => {
      expect(screen.getByText("Hospital invoice")).toBeInTheDocument();
    });
  });
});
