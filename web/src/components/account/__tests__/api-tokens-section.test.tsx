import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { http, HttpResponse } from "msw";
import { ThemeProvider } from "next-themes";
import { toast } from "sonner";

import { renderWithRouter, screen, userEvent, waitFor } from "@/test/test-utils";
import { setToken } from "@/lib/api-client";
import { fakeToken } from "@/test/mocks/handlers";
import { server } from "@/test/mocks/server";

import { ApiTokensSection } from "../api-tokens-section";

vi.mock("sonner", () => ({
  toast: { success: vi.fn(), error: vi.fn(), info: vi.fn() },
}));

function Wrapped() {
  return (
    <ThemeProvider attribute="class" defaultTheme="light" enableSystem>
      <ApiTokensSection />
    </ThemeProvider>
  );
}

beforeEach(() => {
  setToken(fakeToken);
  vi.mocked(toast.success).mockClear();
  vi.mocked(toast.error).mockClear();
});

afterEach(() => server.resetHandlers());

describe("ApiTokensSection", () => {
  it("renders the empty state when there are no tokens", async () => {
    renderWithRouter(<Wrapped />);
    expect(await screen.findByText(/no tokens yet/i)).toBeInTheDocument();
  });

  it("lists existing tokens with their metadata", async () => {
    server.use(
      http.get("*/api/tokens", () =>
        HttpResponse.json({
          data: [
            {
              id: "t1",
              name: "claude agent",
              user_id: "u1",
              created_at: "2026-06-01T00:00:00Z",
              last_used_at: "2026-06-04T00:00:00Z",
              expires_at: null,
            },
          ],
        }),
      ),
    );
    renderWithRouter(<Wrapped />);
    expect(await screen.findByText("claude agent")).toBeInTheDocument();
    expect(screen.getByText(/no expiry/i)).toBeInTheDocument();
  });

  it("flags an expired token", async () => {
    server.use(
      http.get("*/api/tokens", () =>
        HttpResponse.json({
          data: [
            {
              id: "t1",
              name: "old token",
              user_id: "u1",
              created_at: "2025-01-01T00:00:00Z",
              expires_at: "2025-02-01T00:00:00Z",
            },
          ],
        }),
      ),
    );
    renderWithRouter(<Wrapped />);
    expect(await screen.findByText("old token")).toBeInTheDocument();
    // "expired" appears both as the badge and in the meta line.
    expect(screen.getAllByText(/expired/i).length).toBeGreaterThanOrEqual(1);
  });

  it("creates a token and reveals the secret once", async () => {
    server.use(
      http.get("*/api/tokens", () => HttpResponse.json({ data: [] })),
      http.post("*/api/tokens", async ({ request }) => {
        const body = (await request.json()) as { name: string };
        return HttpResponse.json(
          {
            data: {
              token: "nexus_pat_secretvalue123",
              meta: {
                id: "t-new",
                name: body.name,
                user_id: "u1",
                created_at: "2026-06-05T00:00:00Z",
              },
            },
          },
          { status: 201 },
        );
      }),
    );
    renderWithRouter(<Wrapped />);

    await userEvent.click(
      await screen.findByRole("button", { name: /new token/i }),
    );
    const nameInput = await screen.findByLabelText(/token name/i);
    await userEvent.type(nameInput, "my agent");
    await userEvent.click(screen.getByRole("button", { name: /create token/i }));

    await waitFor(() =>
      expect(screen.getByText("nexus_pat_secretvalue123")).toBeInTheDocument(),
    );
    expect(screen.getByText(/only time the token is shown/i)).toBeInTheDocument();
    expect(toast.success).toHaveBeenCalledWith("Token created");
  });

  it("revokes a token after typing its name to confirm", async () => {
    let deleted = false;
    server.use(
      http.get("*/api/tokens", () =>
        HttpResponse.json({
          data: deleted
            ? []
            : [
                {
                  id: "t1",
                  name: "doomed",
                  user_id: "u1",
                  created_at: "2026-06-01T00:00:00Z",
                },
              ],
        }),
      ),
      http.delete("*/api/tokens/t1", () => {
        deleted = true;
        return new HttpResponse(null, { status: 204 });
      }),
    );
    renderWithRouter(<Wrapped />);

    await userEvent.click(
      await screen.findByRole("button", { name: /revoke doomed/i }),
    );
    // Type the token name to arm the destructive action.
    const confirmInput = await screen.findByPlaceholderText("doomed");
    await userEvent.type(confirmInput, "doomed");
    await userEvent.click(
      screen.getByRole("button", { name: /revoke token/i }),
    );

    await waitFor(() =>
      expect(toast.success).toHaveBeenCalledWith("Token revoked"),
    );
  });
});
