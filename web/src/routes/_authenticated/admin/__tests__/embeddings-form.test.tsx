import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { http, HttpResponse } from "msw";
import { toast } from "sonner";

import { render, screen, userEvent, waitFor } from "@/test/test-utils";
import { server } from "@/test/mocks/server";
import { setToken } from "@/lib/api-client";
import { EmbeddingsForm } from "../settings";

vi.mock("sonner", () => ({
  toast: { success: vi.fn(), error: vi.fn(), info: vi.fn() },
}));

const savedVoyage = {
  provider: "voyage",
  model: "voyage-4-large",
  api_key: "****ckey",
  ollama_url: "http://localhost:11434",
};

const savedOllama = {
  provider: "ollama",
  model: "nomic-embed-text",
  api_key: "",
  ollama_url: "http://localhost:11434",
};

const savedNone = {
  provider: "",
  model: "",
  api_key: "",
  ollama_url: "http://localhost:11434",
};

function mockStats(overrides: Partial<{ total_documents: number }> = {}) {
  const total = overrides.total_documents ?? 0;
  server.use(
    http.get("*/api/admin/stats", () =>
      HttpResponse.json({
        data: {
          total_documents: total,
          per_source: [],
          embedding: {
            provider: "voyage",
            model: "voyage-4-large",
            dimension: 1024,
          },
        },
      }),
    ),
  );
}

beforeEach(() => {
  setToken("tok");
  vi.mocked(toast.success).mockClear();
  vi.mocked(toast.error).mockClear();
});
afterEach(() => server.resetHandlers());

describe("EmbeddingsForm", () => {
  it("renders BM25-only hint when provider is empty", async () => {
    mockStats();
    server.use(
      http.get("*/api/settings/embedding", () =>
        HttpResponse.json({ data: savedNone }),
      ),
    );
    render(<EmbeddingsForm />);
    await waitFor(() =>
      expect(
        screen.getByText(/No embeddings — search falls back to BM25/),
      ).toBeInTheDocument(),
    );
    // Model field + API key field both hidden while provider is empty.
    expect(screen.queryByLabelText(/Model/)).not.toBeInTheDocument();
  });

  it("shows the Ollama URL field when provider is ollama", async () => {
    mockStats();
    server.use(
      http.get("*/api/settings/embedding", () =>
        HttpResponse.json({ data: savedOllama }),
      ),
    );
    render(<EmbeddingsForm />);
    await waitFor(() =>
      expect(
        screen.getByPlaceholderText("http://localhost:11434"),
      ).toBeInTheDocument(),
    );
    // API-key-only copy shouldn't show up for ollama.
    expect(screen.queryByPlaceholderText(/sk-/i)).not.toBeInTheDocument();
    expect(screen.queryByPlaceholderText(/pa-/i)).not.toBeInTheDocument();
  });

  it("shows the masked key + Replace for voyage", async () => {
    mockStats();
    server.use(
      http.get("*/api/settings/embedding", () =>
        HttpResponse.json({ data: savedVoyage }),
      ),
    );
    render(<EmbeddingsForm />);
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: /replace/i }),
      ).toBeInTheDocument(),
    );
    expect(screen.getByText("ckey")).toBeInTheDocument();
  });

  it("Replace reveals an empty password input + Cancel restores masked state", async () => {
    mockStats();
    server.use(
      http.get("*/api/settings/embedding", () =>
        HttpResponse.json({ data: savedVoyage }),
      ),
    );
    render(<EmbeddingsForm />);
    await userEvent.click(
      await screen.findByRole("button", { name: /replace/i }),
    );
    const input = await screen.findByPlaceholderText(/pa-/i);
    expect(input).toBeInTheDocument();

    const cancel = await screen.findByRole("button", { name: /cancel/i });
    await userEvent.click(cancel);
    await waitFor(() =>
      expect(
        screen.queryByRole("button", { name: /cancel/i }),
      ).not.toBeInTheDocument(),
    );
    expect(
      await screen.findByRole("button", { name: /replace/i }),
    ).toBeInTheDocument();
  });

  it("switching provider cycles model + clears api_key, cycling back restores saved", async () => {
    mockStats();
    server.use(
      http.get("*/api/settings/embedding", () =>
        HttpResponse.json({ data: savedVoyage }),
      ),
    );
    render(<EmbeddingsForm />);
    const providerTrigger = await waitFor(() => {
      const el = document.querySelector<HTMLElement>(
        '[data-slot="select-trigger"]',
      );
      if (!el) throw new Error("provider select not ready");
      return el;
    });
    // Switch to "No embeddings" — handleProviderChange hits the
    // non-returning-to-saved branch.
    await userEvent.click(providerTrigger);
    await userEvent.click(
      await screen.findByRole("option", { name: /Disabled/i }),
    );
    await waitFor(() =>
      expect(
        screen.getByText(/No embeddings — search falls back to BM25/),
      ).toBeInTheDocument(),
    );
    // Cycle back to Voyage — returningToSaved branch restores masked key.
    await userEvent.click(providerTrigger);
    await userEvent.click(
      await screen.findByRole("option", { name: /Voyage/i }),
    );
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: /replace/i }),
      ).toBeInTheDocument(),
    );
  });

  it("changing the provider pops the full-reindex confirm; Cancel leaves the form dirty", async () => {
    mockStats({ total_documents: 1234 });
    server.use(
      http.get("*/api/settings/embedding", () =>
        HttpResponse.json({ data: savedVoyage }),
      ),
    );
    render(<EmbeddingsForm />);
    const providerTrigger = await waitFor(() => {
      const el = document.querySelector<HTMLElement>(
        '[data-slot="select-trigger"]',
      );
      if (!el) throw new Error("provider select not ready");
      return el;
    });
    await userEvent.click(providerTrigger);
    await userEvent.click(
      await screen.findByRole("option", { name: /Ollama/i }),
    );
    // Save button now visible (dirty); re-index warning also appears.
    const save = await screen.findByRole("button", { name: /save changes/i });
    await userEvent.click(save);
    // Confirm dialog: Re-index?
    expect(
      await screen.findByRole("heading", { name: /Full re-index/i }),
    ).toBeInTheDocument();
    // Cancel closes the dialog but keeps draft dirty.
    await userEvent.click(screen.getByRole("button", { name: /^cancel$/i }));
    await waitFor(() =>
      expect(
        screen.queryByRole("heading", { name: /Full re-index/i }),
      ).not.toBeInTheDocument(),
    );
    expect(
      screen.getByRole("button", { name: /save changes/i }),
    ).toBeInTheDocument();
  });

  it("confirming the re-index dialog fires PUT /api/settings/embedding", async () => {
    mockStats({ total_documents: 0 });
    let received: { provider?: string } | null = null;
    server.use(
      http.get("*/api/settings/embedding", () =>
        HttpResponse.json({ data: savedVoyage }),
      ),
      http.put("*/api/settings/embedding", async ({ request }) => {
        received = (await request.json()) as { provider?: string };
        return HttpResponse.json({
          data: { ...savedVoyage, provider: received?.provider ?? "voyage" },
        });
      }),
    );
    render(<EmbeddingsForm />);
    const providerTrigger = await waitFor(() => {
      const el = document.querySelector<HTMLElement>(
        '[data-slot="select-trigger"]',
      );
      if (!el) throw new Error("provider select not ready");
      return el;
    });
    await userEvent.click(providerTrigger);
    await userEvent.click(
      await screen.findByRole("option", { name: /Ollama/i }),
    );
    await userEvent.click(
      await screen.findByRole("button", { name: /save changes/i }),
    );
    await screen.findByRole("heading", { name: /Full re-index/i });
    await userEvent.click(
      screen.getByRole("button", { name: /save & re-index/i }),
    );
    await waitFor(() => expect(received).not.toBeNull());
    expect(received!.provider).toBe("ollama");
    expect(toast.success).toHaveBeenCalledWith("Embedding settings saved");
  });

  it("non-critical change (ollama_url) submits directly without the confirm dialog", async () => {
    mockStats();
    let received: { ollama_url?: string } | null = null;
    server.use(
      http.get("*/api/settings/embedding", () =>
        HttpResponse.json({ data: savedOllama }),
      ),
      http.put("*/api/settings/embedding", async ({ request }) => {
        received = (await request.json()) as { ollama_url?: string };
        return HttpResponse.json({
          data: { ...savedOllama, ollama_url: received?.ollama_url ?? "" },
        });
      }),
    );
    render(<EmbeddingsForm />);
    const urlInput = await screen.findByPlaceholderText("http://localhost:11434");
    await userEvent.clear(urlInput);
    await userEvent.type(urlInput, "http://ollama.example:11434");
    await userEvent.click(
      await screen.findByRole("button", { name: /save changes/i }),
    );
    // No confirm dialog for a non-provider/model change.
    expect(
      screen.queryByRole("heading", { name: /Full re-index/i }),
    ).not.toBeInTheDocument();
    await waitFor(() => expect(received).not.toBeNull());
    expect(received!.ollama_url).toBe("http://ollama.example:11434");
  });
});
