import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  createMemoryHistory,
  createRouter,
  RouterProvider,
} from "@tanstack/react-router";
import { http, HttpResponse } from "msw";
import { toast } from "sonner";

import { routeTree } from "@/routeTree.gen";
import { server } from "@/test/mocks/server";
import { setToken } from "@/lib/api-client";
import { fakeToken } from "@/test/mocks/handlers";

vi.mock("sonner", () => ({
  toast: { success: vi.fn(), error: vi.fn(), info: vi.fn() },
}));

const originalMatchMedia = window.matchMedia;
function mockMobile(mobile: boolean) {
  window.matchMedia = vi.fn().mockReturnValue({
    matches: mobile,
    media: "",
    onchange: null,
    addListener: vi.fn(),
    removeListener: vi.fn(),
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    dispatchEvent: vi.fn(),
  }) as unknown as typeof window.matchMedia;
}

function setup() {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0, staleTime: 0 },
      mutations: { retry: false },
    },
  });
  const router = createRouter({
    routeTree,
    context: { queryClient },
    history: createMemoryHistory({ initialEntries: ["/admin/users"] }),
  });
  render(
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
  return router;
}

const seeded = [
  {
    id: "u1",
    username: "admin",
    role: "admin",
    created_at: "2026-01-01T00:00:00Z",
  },
  {
    id: "u2",
    username: "alice",
    role: "user",
    created_at: "2026-02-01T00:00:00Z",
  },
];

beforeEach(() => {
  setToken(fakeToken);
  mockMobile(false);
  vi.mocked(toast.success).mockClear();
  vi.mocked(toast.error).mockClear();
});

afterEach(() => {
  server.resetHandlers();
  window.matchMedia = originalMatchMedia;
});

describe("admin/users page", () => {
  it("renders the desktop table with both rows", async () => {
    server.use(
      http.get("*/api/users", () => HttpResponse.json({ data: seeded })),
    );
    setup();
    // Wait for data to render — useUsers fetches async so the heading
    // appears before the rows.
    await waitFor(() =>
      expect(screen.getByText("alice")).toBeInTheDocument(),
    );
    expect(screen.getAllByText(/admin/i).length).toBeGreaterThan(0);
  });

  it("renders the mobile list when useIsMobile() is true", async () => {
    mockMobile(true);
    server.use(
      http.get("*/api/users", () => HttpResponse.json({ data: seeded })),
    );
    setup();
    await waitFor(() =>
      expect(screen.getByText("alice")).toBeInTheDocument(),
    );
  });

  it("shows the empty-roster hint when the list is empty", async () => {
    server.use(http.get("*/api/users", () => HttpResponse.json({ data: [] })));
    setup();
    await waitFor(() =>
      expect(screen.getByText(/No users yet/i)).toBeInTheDocument(),
    );
  });

  it("New user sheet opens, submits, toasts, and closes", async () => {
    let created = false;
    server.use(
      http.get("*/api/users", () =>
        HttpResponse.json({
          data: created
            ? [
                ...seeded,
                {
                  id: "u3",
                  username: "bob",
                  role: "user",
                  created_at: "2026-04-19T00:00:00Z",
                },
              ]
            : seeded,
        }),
      ),
      http.post("*/api/users", () => {
        created = true;
        return HttpResponse.json(
          {
            data: {
              id: "u3",
              username: "bob",
              role: "user",
              created_at: "2026-04-19T00:00:00Z",
            },
          },
          { status: 201 },
        );
      }),
    );
    setup();
    await waitFor(() =>
      expect(screen.getByText("alice")).toBeInTheDocument(),
    );

    await userEvent.click(screen.getByRole("button", { name: /new user/i }));
    // The sheet uses explicit ids for its fields (avoiding clashes with the
    // change-password sheet that's ambient on this page).
    const username = (await screen.findByLabelText(
      /username/i,
    )) as HTMLInputElement;
    await userEvent.type(username, "bob");
    const password = document.getElementById(
      "new-password",
    ) as HTMLInputElement;
    await userEvent.type(password, "hunter2-hunter2");
    await userEvent.click(
      screen.getByRole("button", { name: /create user/i }),
    );
    await waitFor(() => expect(created).toBe(true));
    expect(toast.success).toHaveBeenCalledWith("User created");
  });
});
