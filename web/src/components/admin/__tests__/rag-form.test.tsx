import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { http, HttpResponse } from "msw";
import { toast } from "sonner";
import { render, screen, userEvent, waitFor } from "@/test/test-utils";

import { RAGForm } from "../rag-form";
import { server } from "@/test/mocks/server";
import { setToken } from "@/lib/api-client";
import type { RAGSettings } from "@/lib/api-types";

const SAVED: RAGSettings = {
  max_tool_rounds: 3,
  max_images_per_turn: 4,
  enable_multimodal: true,
  enable_open_attachment: false,
};

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
    http.get("*/api/settings/rag", () => HttpResponse.json({ data: SAVED })),
  );
});
afterEach(() => server.resetHandlers());

const rounds = () =>
  screen.findByRole("spinbutton", { name: "Max tool rounds per turn" });

describe("RAGForm", () => {
  it("renders the persisted settings", async () => {
    mount();
    expect(((await rounds()) as HTMLInputElement).value).toBe("3");
    const images = screen.getByRole("spinbutton", { name: "Max images per turn" });
    expect((images as HTMLInputElement).value).toBe("4");
    expect(screen.queryByText(/Draft · not saved yet/)).not.toBeInTheDocument();
  });

  it("flips into dirty state when the value changes and reverts on Revert", async () => {
    const user = userEvent.setup();
    mount();
    const input = await rounds();
    await user.clear(input);
    await user.type(input, "1");
    expect(screen.getByText(/Draft · not saved yet/)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: /Revert/ }));
    expect((input as HTMLInputElement).value).toBe("3");
    expect(screen.queryByText(/Draft · not saved yet/)).not.toBeInTheDocument();
  });

  it("PUTs the full settings shape and shows the success toast", async () => {
    const user = userEvent.setup();
    let putBody: RAGSettings | undefined;
    server.use(
      http.put("*/api/settings/rag", async ({ request }) => {
        putBody = (await request.json()) as RAGSettings;
        return HttpResponse.json({ data: { ...SAVED, max_tool_rounds: 4 } });
      }),
    );
    mount();
    const input = await rounds();
    await user.clear(input);
    await user.type(input, "4");
    await user.click(screen.getByRole("button", { name: /Save changes/ }));
    await waitFor(() => expect(toast.success).toHaveBeenCalled());
    expect(putBody).toEqual({
      max_tool_rounds: 4,
      max_images_per_turn: 4,
      enable_multimodal: true,
      enable_open_attachment: false,
    });
  });

  it("toggles the open-attachment switch into the PUT body", async () => {
    const user = userEvent.setup();
    let putBody: RAGSettings | undefined;
    server.use(
      http.put("*/api/settings/rag", async ({ request }) => {
        putBody = (await request.json()) as RAGSettings;
        return HttpResponse.json({ data: SAVED });
      }),
    );
    mount();
    await rounds();
    await user.click(
      screen.getByRole("switch", { name: /nexus_open_attachment/ }),
    );
    expect(screen.getByText(/Draft · not saved yet/)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: /Save changes/ }));
    await waitFor(() => expect(toast.success).toHaveBeenCalled());
    expect(putBody?.enable_open_attachment).toBe(true);
  });

  it("disables the images input when multimodal is off", async () => {
    const user = userEvent.setup();
    mount();
    await rounds();
    const images = screen.getByRole("spinbutton", { name: "Max images per turn" });
    expect(images).not.toBeDisabled();
    await user.click(
      screen.getByRole("switch", { name: /Attach images/ }),
    );
    expect(images).toBeDisabled();
  });

  it("disables Save when a value is out of range", async () => {
    const user = userEvent.setup();
    mount();
    const input = await rounds();
    await user.clear(input);
    await user.type(input, "9");
    const saveBtn = screen.getByRole("button", { name: /Save changes/ });
    expect(saveBtn).toBeDisabled();
    expect(input).toHaveClass("border-destructive/60");
  });
});
