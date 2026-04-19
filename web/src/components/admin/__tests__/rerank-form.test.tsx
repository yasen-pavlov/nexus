import {
  afterEach,
  beforeEach,
  describe,
  expect,
  it,
  vi,
} from "vitest";
import { http, HttpResponse } from "msw";
import { toast } from "sonner";

import { fireEvent } from "@testing-library/react";
import { render, screen, userEvent, waitFor } from "@/test/test-utils";
import { server } from "@/test/mocks/server";
import { setToken } from "@/lib/api-client";
import { RerankForm } from "../rerank-form";

vi.mock("sonner", () => ({
  toast: { success: vi.fn(), error: vi.fn(), info: vi.fn() },
}));

const savedVoyage = {
  provider: "voyage",
  model: "voyage-rerank-2",
  api_key: "****last",
  min_score: 0.4,
};

const savedNone = { provider: "", model: "", api_key: "", min_score: 0.4 };

beforeEach(() => {
  setToken("tok");
  vi.mocked(toast.success).mockClear();
  vi.mocked(toast.error).mockClear();
});
afterEach(() => server.resetHandlers());

describe("RerankForm", () => {
  it("hides model + score-floor fields when provider is empty", async () => {
    server.use(
      http.get("*/api/settings/rerank", () =>
        HttpResponse.json({ data: savedNone }),
      ),
    );
    render(<RerankForm />);
    await waitFor(() =>
      expect(screen.getByText(/No reranking/)).toBeInTheDocument(),
    );
    expect(screen.queryByLabelText("Rerank score floor")).not.toBeInTheDocument();
  });

  it("renders masked API key + Replace button for voyage", async () => {
    server.use(
      http.get("*/api/settings/rerank", () =>
        HttpResponse.json({ data: savedVoyage }),
      ),
    );
    render(<RerankForm />);
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: /replace/i }),
      ).toBeInTheDocument(),
    );
    expect(screen.getByText("last")).toBeInTheDocument();
  });

  it("Replace reveals an empty password input", async () => {
    server.use(
      http.get("*/api/settings/rerank", () =>
        HttpResponse.json({ data: savedVoyage }),
      ),
    );
    render(<RerankForm />);
    await userEvent.click(
      await screen.findByRole("button", { name: /replace/i }),
    );
    await waitFor(() =>
      expect(
        screen.getByPlaceholderText(/paste your key/i),
      ).toBeInTheDocument(),
    );
    expect(
      screen.getByRole("button", { name: /cancel/i }),
    ).toBeInTheDocument();
  });

  it("replacing the API key enables Save and PUTs the new value", async () => {
    let received: { api_key?: string } | null = null;
    server.use(
      http.get("*/api/settings/rerank", () =>
        HttpResponse.json({ data: savedVoyage }),
      ),
      http.put("*/api/settings/rerank", async ({ request }) => {
        received = (await request.json()) as { api_key?: string };
        return HttpResponse.json({
          data: { ...savedVoyage, api_key: "****ast" },
        });
      }),
    );

    render(<RerankForm />);
    await userEvent.click(
      await screen.findByRole("button", { name: /replace/i }),
    );
    const input = await screen.findByPlaceholderText(/paste your key/i);
    await userEvent.type(input, "new-rerank-key");

    const save = await screen.findByRole("button", { name: /save changes/i });
    await waitFor(() => expect(save).toBeEnabled());
    await userEvent.click(save);

    await waitFor(() => expect(received).not.toBeNull());
    expect(received!.api_key).toBe("new-rerank-key");
    expect(toast.success).toHaveBeenCalledWith("Reranking settings saved");
  });

  it("Cancel during replace restores the masked key without dirtying", async () => {
    server.use(
      http.get("*/api/settings/rerank", () =>
        HttpResponse.json({ data: savedVoyage }),
      ),
    );
    render(<RerankForm />);
    await userEvent.click(
      await screen.findByRole("button", { name: /replace/i }),
    );
    await screen.findByPlaceholderText(/paste your key/i);
    // Cancel button only renders while replacingKey && saved?.api_key.
    const cancel = await screen.findByRole("button", { name: /cancel/i });
    await userEvent.click(cancel);
    await waitFor(() =>
      expect(
        screen.queryByRole("button", { name: /cancel/i }),
      ).not.toBeInTheDocument(),
    );
    // Masked display is back; no Save/Revert since nothing is dirty.
    expect(
      await screen.findByRole("button", { name: /replace/i }),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: /save changes/i }),
    ).not.toBeInTheDocument();
  });

  it("slider move enables Save and submits the new min_score", async () => {
    let received: { min_score?: number } | null = null;
    server.use(
      http.get("*/api/settings/rerank", () =>
        HttpResponse.json({ data: savedVoyage }),
      ),
      http.put("*/api/settings/rerank", async ({ request }) => {
        received = (await request.json()) as { min_score?: number };
        return HttpResponse.json({
          data: { ...savedVoyage, min_score: received?.min_score ?? 0.4 },
        });
      }),
    );
    render(<RerankForm />);
    const slider = await screen.findByLabelText("Rerank score floor");
    const input = slider.querySelector(
      'input[type="range"]',
    ) as HTMLInputElement;
    fireEvent.change(input, { target: { value: "55" } });

    const save = await screen.findByRole("button", { name: /save changes/i });
    await waitFor(() => expect(save).toBeEnabled());
    await userEvent.click(save);

    await waitFor(() => expect(received).not.toBeNull());
    expect(received!.min_score).toBeCloseTo(0.55, 2);
  });

  it("switching provider away and back restores the saved draft", async () => {
    server.use(
      http.get("*/api/settings/rerank", () =>
        HttpResponse.json({ data: savedVoyage }),
      ),
    );
    render(<RerankForm />);
    // The Provider select is a shadcn Select. The on-screen Slider and
    // ModelCombobox also get role="combobox", so scope by data-slot.
    const providerTrigger = await waitFor(() => {
      const el = document.querySelector<HTMLElement>(
        '[data-slot="select-trigger"]',
      );
      if (!el) throw new Error("provider select not ready");
      return el;
    });
    await userEvent.click(providerTrigger);
    await userEvent.click(
      await screen.findByRole("option", { name: /disabled/i }),
    );
    // Score-floor field should be gone once provider === "".
    await waitFor(() =>
      expect(
        screen.queryByLabelText("Rerank score floor"),
      ).not.toBeInTheDocument(),
    );
    // Switch back to Voyage — the returningToSaved branch restores model +
    // masked api_key from saved, re-rendering the Replace button.
    await userEvent.click(providerTrigger);
    await userEvent.click(
      await screen.findByRole("option", { name: /voyage/i }),
    );
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: /replace/i }),
      ).toBeInTheDocument(),
    );
  });

  it("Revert restores the masked key state after typing", async () => {
    server.use(
      http.get("*/api/settings/rerank", () =>
        HttpResponse.json({ data: savedVoyage }),
      ),
    );
    render(<RerankForm />);
    await userEvent.click(
      await screen.findByRole("button", { name: /replace/i }),
    );
    const input = await screen.findByPlaceholderText(/paste your key/i);
    await userEvent.type(input, "abc");
    const revert = await screen.findByRole("button", { name: /revert/i });
    await userEvent.click(revert);
    await waitFor(() =>
      expect(
        screen.queryByRole("button", { name: /revert/i }),
      ).not.toBeInTheDocument(),
    );
    // Masked display restored.
    expect(
      await screen.findByRole("button", { name: /replace/i }),
    ).toBeInTheDocument();
  });
});
