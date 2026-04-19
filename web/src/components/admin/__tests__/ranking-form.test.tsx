import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { http, HttpResponse } from "msw";
import { toast } from "sonner";
import { render, screen, userEvent, waitFor } from "@/test/test-utils";

import { RankingForm } from "../ranking-form";
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
      <RankingForm />
    </QueryClientProvider>,
  );
}

const defaults = {
  source_half_life_days: { telegram: 14, imap: 30, filesystem: 90, paperless: 180 },
  source_recency_floor: { telegram: 0.65, imap: 0.75, filesystem: 0.85, paperless: 0.9 },
  source_trust_weight: { telegram: 0.92, imap: 0.92, filesystem: 1, paperless: 1.05 },
  metadata_bonus_enabled: true,
  source_trust_enabled: true,
  known_source_types: ["imap", "telegram", "paperless", "filesystem"],
};

vi.mock("sonner", () => ({
  toast: { success: vi.fn(), error: vi.fn(), info: vi.fn() },
}));

beforeEach(() => {
  setToken("fake-test-token");
  vi.mocked(toast.success).mockClear();
  vi.mocked(toast.error).mockClear();
  server.use(
    http.get("*/api/settings/ranking", () =>
      HttpResponse.json({ data: defaults }),
    ),
  );
});
afterEach(() => server.resetHandlers());

describe("RankingForm", () => {
  it("renders the Signals card + one card per known source type", async () => {
    mount();
    await waitFor(() =>
      expect(screen.getByText("Apply source trust weights")).toBeInTheDocument(),
    );
    expect(screen.getByText("Apply metadata bonus")).toBeInTheDocument();

    // Each card is an <article> with a tonal --chip-hue css var — that's a
    // stable selector that doesn't depend on which element renders the
    // label text.
    const cards = document.querySelectorAll("article");
    expect(cards.length).toBe(4);
    const hues = Array.from(cards).map((c) =>
      (c as HTMLElement).style.getPropertyValue("--chip-hue"),
    );
    expect(hues).toEqual(
      expect.arrayContaining([
        "var(--source-imap, var(--source-default))",
        "var(--source-telegram, var(--source-default))",
        "var(--source-paperless, var(--source-default))",
        "var(--source-filesystem, var(--source-default))",
      ]),
    );
  });

  it("clicking a preset chip updates the card's knobs + plain-language line", async () => {
    mount();
    await waitFor(() =>
      expect(screen.getByText("Apply metadata bonus")).toBeInTheDocument(),
    );

    const telegramCard = Array.from(document.querySelectorAll("article")).find(
      (c) =>
        (c as HTMLElement).style
          .getPropertyValue("--chip-hue")
          .includes("--source-telegram"),
    );
    expect(telegramCard).toBeTruthy();

    const archiveChip = Array.from(
      telegramCard!.querySelectorAll("button"),
    ).find((b) => b.textContent === "Archive");
    expect(archiveChip).toBeTruthy();

    await userEvent.click(archiveChip!);

    // Archive preset for telegram sets half-life to 60 days (= 2 months)
    // with floor 0.9. The plain-language line should reflect both.
    await waitFor(() =>
      expect(telegramCard!.textContent).toMatch(/Half-relevance after 2 months/),
    );
    expect(telegramCard!.textContent).toMatch(/90%/);
  });

  it("toggling a Signals switch flips aria-checked and surfaces the Draft bar + Save button", async () => {
    // Exercise the dirty-detection path that the save flow ultimately
    // relies on. The PUT round-trip + onSuccess invalidation is covered by
    // the hook's own test (use-ranking-settings.test.tsx) — duplicating
    // it here is brittle because base-ui's <Button type="submit"> +
    // synthetic event path is flaky under happy-dom + userEvent.
    mount();
    await waitFor(() =>
      expect(screen.getByText("Apply metadata bonus")).toBeInTheDocument(),
    );

    const metadataSwitch = screen
      .getByText("Apply metadata bonus")
      .closest('[role="switch"]') as HTMLElement;
    expect(metadataSwitch).toBeTruthy();
    expect(metadataSwitch.getAttribute("aria-checked")).toBe("true");

    await userEvent.click(metadataSwitch);

    await waitFor(() =>
      expect(metadataSwitch.getAttribute("aria-checked")).toBe("false"),
    );

    // Flipping the saved value → dirty → Draft bar mounts with a Save CTA.
    await waitFor(() =>
      expect(screen.getByText(/Draft · not saved yet/)).toBeInTheDocument(),
    );
    expect(screen.getByRole("button", { name: /Save changes/ })).toBeEnabled();
  });

  it("per-source card defaults read-out matches the plain-language contract", async () => {
    mount();
    await waitFor(() =>
      expect(screen.getByText("Apply metadata bonus")).toBeInTheDocument(),
    );
    // Paperless default: 180 days half-life, 0.9 floor, trust +5%.
    const paperlessCard = Array.from(document.querySelectorAll("article")).find(
      (c) =>
        (c as HTMLElement).style
          .getPropertyValue("--chip-hue")
          .includes("--source-paperless"),
    );
    expect(paperlessCard).toBeTruthy();
    expect(paperlessCard!.textContent).toMatch(/Half-relevance after 6 months/);
    expect(paperlessCard!.textContent).toMatch(/90%/);
    expect(paperlessCard!.textContent).toMatch(/\+5%/);
  });
});
