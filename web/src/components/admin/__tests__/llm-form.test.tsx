import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { http, HttpResponse } from "msw";
import { toast } from "sonner";
import { render, screen, userEvent, waitFor } from "@/test/test-utils";

import { LLMForm } from "../llm-form";
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
      <LLMForm />
    </QueryClientProvider>,
  );
}

vi.mock("sonner", () => ({
  toast: { success: vi.fn(), error: vi.fn(), info: vi.fn() },
}));

const seedSettings = {
  default_model: "anthropic:claude-sonnet-4-6",
  anthropic_api_key: "****1234",
  openai_api_key: "",
  ollama_url: "http://localhost:11434",
  allowlist: [],
};

const seedModels = [
  {
    id: "anthropic:claude-sonnet-4-6",
    provider: "anthropic",
    bare_id: "claude-sonnet-4-6",
    display_name: "Claude Sonnet 4.6",
    context_window: 1_000_000,
    supports_citations: true,
    supports_tools: true,
    supports_vision: true,
    supports_caching: true,
    input_cost_per_mtok: 3,
    output_cost_per_mtok: 15,
    typical_ttft_ms: 700,
  },
  {
    id: "anthropic:claude-haiku-4-5",
    provider: "anthropic",
    bare_id: "claude-haiku-4-5",
    display_name: "Claude Haiku 4.5",
    context_window: 200_000,
    supports_citations: true,
    supports_tools: true,
    supports_vision: true,
    supports_caching: true,
    input_cost_per_mtok: 1,
    output_cost_per_mtok: 5,
    typical_ttft_ms: 400,
  },
];

beforeEach(() => {
  setToken("fake-test-token");
  vi.mocked(toast.success).mockClear();
  vi.mocked(toast.error).mockClear();
  server.use(
    http.get("*/api/settings/llm", () =>
      HttpResponse.json({ data: seedSettings }),
    ),
    http.get("*/api/llm/models", () => HttpResponse.json({ data: seedModels })),
  );
});
afterEach(() => server.resetHandlers());

describe("LLMForm", () => {
  it("renders default model + masked Anthropic key on initial load", async () => {
    mount();
    await waitFor(() =>
      expect(screen.getByText("Default model")).toBeInTheDocument(),
    );

    // The bare combobox shows the saved bare model id.
    expect(screen.getByDisplayValue("claude-sonnet-4-6")).toBeInTheDocument();

    // Masked key chrome — tail of the seed key, plus an Anthropic Replace
    // button (one Replace per masked key; OpenAI key is empty so its field
    // renders the password input instead).
    expect(screen.getByText("1234")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /replace/i })).toBeInTheDocument();
  });

  it("replacing the Anthropic key clears the input and reveals Cancel", async () => {
    mount();
    await waitFor(() =>
      expect(screen.getByRole("button", { name: /replace/i })).toBeInTheDocument(),
    );

    await userEvent.click(screen.getByRole("button", { name: /replace/i }));

    // After clicking Replace, BOTH provider key fields are now password
    // inputs (Anthropic just opened, OpenAI was already empty). Just count.
    const inputs = screen.getAllByPlaceholderText(/paste your key or leave blank/i);
    expect(inputs.length).toBe(2);
    expect(screen.getByRole("button", { name: /cancel/i })).toBeInTheDocument();
  });

  it("PUTs the form on save and shows a success toast", async () => {
    let captured: unknown = null;
    server.use(
      http.put("*/api/settings/llm", async ({ request }) => {
        captured = await request.json();
        return HttpResponse.json({ data: seedSettings });
      }),
    );

    mount();
    await waitFor(() =>
      expect(screen.getByText("Default model")).toBeInTheDocument(),
    );

    // Toggle the Ollama URL — that drives the form dirty without poking the
    // masked-key flow (which has its own test above).
    const ollamaInput = screen.getByPlaceholderText("http://localhost:11434");
    await userEvent.clear(ollamaInput);
    await userEvent.type(ollamaInput, "http://ollama.lan:11434");

    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: /save changes/i }),
      ).toBeInTheDocument(),
    );

    await userEvent.click(
      screen.getByRole("button", { name: /save changes/i }),
    );
    await waitFor(() => expect(captured).not.toBeNull());

    const body = captured as Record<string, unknown>;
    expect(body.ollama_url).toBe("http://ollama.lan:11434");
    expect(body.anthropic_api_key).toBe("****1234"); // masked round-trip preserves
    await waitFor(() =>
      expect(vi.mocked(toast.success)).toHaveBeenCalled(),
    );
  });

  it("renders the model allowlist with capability pills", async () => {
    mount();
    await waitFor(() =>
      expect(
        screen.getByRole("checkbox", { name: "Claude Sonnet 4.6" }),
      ).toBeInTheDocument(),
    );

    expect(
      screen.getByRole("checkbox", { name: "Claude Haiku 4.5" }),
    ).toBeInTheDocument();

    // Capability pills — one per row per supported capability.
    expect(screen.getAllByText("vision").length).toBeGreaterThanOrEqual(2);
    expect(screen.getAllByText("tools").length).toBeGreaterThanOrEqual(2);
    expect(screen.getAllByText("citations").length).toBeGreaterThanOrEqual(2);
  });

  it("revert restores the saved snapshot", async () => {
    mount();
    await waitFor(() =>
      expect(
        screen.getByPlaceholderText("http://localhost:11434"),
      ).toBeInTheDocument(),
    );

    const ollama = screen.getByPlaceholderText("http://localhost:11434");
    await userEvent.clear(ollama);
    await userEvent.type(ollama, "http://changed:11434");

    await waitFor(() =>
      expect(screen.getByRole("button", { name: /revert/i })).toBeInTheDocument(),
    );
    await userEvent.click(screen.getByRole("button", { name: /revert/i }));

    expect(
      screen.getByDisplayValue("http://localhost:11434"),
    ).toBeInTheDocument();
  });

  it("toggling off a model in the empty-allowlist state switches to a concrete allowlist", async () => {
    let captured: Record<string, unknown> | null = null;
    server.use(
      http.put("*/api/settings/llm", async ({ request }) => {
        captured = (await request.json()) as Record<string, unknown>;
        return HttpResponse.json({ data: seedSettings });
      }),
    );

    mount();
    await waitFor(() =>
      expect(screen.getByText("anthropic:claude-haiku-4-5")).toBeInTheDocument(),
    );

    // The Haiku checkbox is one of the rows; aria-label is the display_name.
    const haiku = screen.getByRole("checkbox", { name: "Claude Haiku 4.5" }) as HTMLInputElement;
    expect(haiku.checked).toBe(true); // empty allowlist = expose all

    await userEvent.click(haiku);
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: /save changes/i }),
      ).toBeInTheDocument(),
    );
    await userEvent.click(
      screen.getByRole("button", { name: /save changes/i }),
    );

    await waitFor(() => expect(captured).not.toBeNull());
    const body = captured as unknown as Record<string, unknown>;
    expect(body.allowlist).toEqual(["anthropic:claude-sonnet-4-6"]);
  });
});
