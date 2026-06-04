import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { http, HttpResponse } from "msw";
import { toast } from "sonner";

import { fireEvent } from "@testing-library/react";
import { render, screen, userEvent, waitFor } from "@/test/test-utils";
import { server } from "@/test/mocks/server";
import { setToken } from "@/lib/api-client";
import { RetentionForm } from "../retention-form";

vi.mock("sonner", () => ({
  toast: { success: vi.fn(), error: vi.fn(), info: vi.fn() },
}));

const saved = {
  retention_days: 90,
  retention_per_connector: 200,
  sweep_interval_minutes: 60,
  min_sweep_interval_minutes: 5,
};

beforeEach(() => {
  setToken("tok");
  vi.mocked(toast.success).mockClear();
  vi.mocked(toast.error).mockClear();
  server.use(
    http.get("*/api/settings/retention", () =>
      HttpResponse.json({ data: saved }),
    ),
  );
});
afterEach(() => server.resetHandlers());

describe("RetentionForm", () => {
  it("renders the three number fields seeded from saved settings", async () => {
    render(<RetentionForm />);
    const fields = await screen.findAllByRole("spinbutton");
    expect(fields).toHaveLength(3);
    expect(fields[0]).toHaveValue(90);
    expect(fields[1]).toHaveValue(200);
    expect(fields[2]).toHaveValue(60);
    // Pristine — no draft bar.
    expect(
      screen.queryByRole("button", { name: /save changes/i }),
    ).not.toBeInTheDocument();
  });

  it("editing a field reveals the draft bar and Save PUTs the form", async () => {
    let received: typeof saved | null = null;
    server.use(
      http.put("*/api/settings/retention", async ({ request }) => {
        received = (await request.json()) as typeof saved;
        return HttpResponse.json({ data: { ...saved, ...received } });
      }),
    );

    render(<RetentionForm />);
    const days = (await screen.findAllByRole("spinbutton"))[0];
    fireEvent.change(days, { target: { value: "30" } });

    const save = await screen.findByRole("button", { name: /save changes/i });
    await waitFor(() => expect(save).toBeEnabled());
    expect(screen.getByText(/Draft · not saved yet/i)).toBeInTheDocument();
    await userEvent.click(save);

    await waitFor(() => expect(received).not.toBeNull());
    expect(received!.retention_days).toBe(30);
    expect(toast.success).toHaveBeenCalledWith("Retention settings saved");
  });

  it("Revert discards the draft", async () => {
    render(<RetentionForm />);
    const days = (await screen.findAllByRole("spinbutton"))[0];
    fireEvent.change(days, { target: { value: "7" } });
    const revert = await screen.findByRole("button", { name: /revert/i });
    await userEvent.click(revert);
    await waitFor(() =>
      expect(
        screen.queryByRole("button", { name: /save changes/i }),
      ).not.toBeInTheDocument(),
    );
    expect((await screen.findAllByRole("spinbutton"))[0]).toHaveValue(90);
  });

  it("a sweep-interval below the minimum disables Save", async () => {
    render(<RetentionForm />);
    const sweep = (await screen.findAllByRole("spinbutton"))[2];
    fireEvent.change(sweep, { target: { value: "1" } });
    const save = await screen.findByRole("button", { name: /save changes/i });
    expect(save).toBeDisabled();
  });

  it("Run now triggers the sweep endpoint", async () => {
    let swept = false;
    server.use(
      http.post("*/api/settings/retention/sweep", () => {
        swept = true;
        return HttpResponse.json({ data: { ok: true } });
      }),
    );
    render(<RetentionForm />);
    const run = await screen.findByRole("button", { name: /run now/i });
    await userEvent.click(run);
    await waitFor(() => expect(swept).toBe(true));
    expect(toast.success).toHaveBeenCalledWith("Cleanup complete");
  });
});
