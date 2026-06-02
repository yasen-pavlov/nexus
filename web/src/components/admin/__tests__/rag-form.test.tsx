import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { http, HttpResponse } from "msw";
import { toast } from "sonner";
import { render, screen, userEvent, waitFor } from "@/test/test-utils";

import { RAGForm } from "../rag-form";
import { server } from "@/test/mocks/server";
import { setToken } from "@/lib/api-client";

function mount() {
  const client = new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0, staleTime: 0 },
      mutations: { retry: false },
    },
  });
  return render(
    <QueryClientProvider client={client}>
      <RAGForm />
    </QueryClientProvider>,
  );
}

vi.mock("sonner", () => ({
  toast: { success: vi.fn(), error: vi.fn(), info: vi.fn() },
}));

beforeEach(() => {
  setToken("fake-test-token");
  vi.mocked(toast.success).mockClear();
  vi.mocked(toast.error).mockClear();
  server.use(
    http.get("*/api/settings/rag", () =>
      HttpResponse.json({ data: { max_tool_rounds: 3 } }),
    ),
  );
});
afterEach(() => server.resetHandlers());

describe("RAGForm", () => {
  it("renders the persisted max_tool_rounds value", async () => {
    mount();
    const input = await screen.findByRole("spinbutton");
    expect((input as HTMLInputElement).value).toBe("3");
    // No dirty footer until the user changes the value.
    expect(screen.queryByText(/Draft · not saved yet/)).not.toBeInTheDocument();
  });

  it("flips into dirty state when the value changes and reverts on Revert", async () => {
    const user = userEvent.setup();
    mount();
    const input = await screen.findByRole("spinbutton");
    await user.clear(input);
    await user.type(input, "1");
    expect(screen.getByText(/Draft · not saved yet/)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: /Revert/ }));
    expect((input as HTMLInputElement).value).toBe("3");
    expect(screen.queryByText(/Draft · not saved yet/)).not.toBeInTheDocument();
  });

  it("PUTs the new value and shows the success toast", async () => {
    const user = userEvent.setup();
    let putBody: { max_tool_rounds?: number } | undefined;
    server.use(
      http.put("*/api/settings/rag", async ({ request }) => {
        putBody = (await request.json()) as { max_tool_rounds?: number };
        return HttpResponse.json({ data: { max_tool_rounds: 4 } });
      }),
    );
    mount();
    const input = await screen.findByRole("spinbutton");
    await user.clear(input);
    await user.type(input, "4");
    await user.click(screen.getByRole("button", { name: /Save changes/ }));
    await waitFor(() => expect(toast.success).toHaveBeenCalled());
    expect(putBody).toEqual({ max_tool_rounds: 4 });
  });

  it("disables Save when the value is out of range", async () => {
    const user = userEvent.setup();
    mount();
    const input = await screen.findByRole("spinbutton");
    await user.clear(input);
    await user.type(input, "9");
    const saveBtn = screen.getByRole("button", { name: /Save changes/ });
    expect(saveBtn).toBeDisabled();
    expect(input).toHaveClass("border-destructive/60");
  });
});
