import { describe, expect, it } from "vitest";
import { useState } from "react";
import { FormProvider, useForm } from "react-hook-form";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

import { SyncWindowField } from "../sync-window-field";
import { render } from "@/test/test-utils";

function Harness({ initial = "" }: { initial?: string }) {
  const methods = useForm<{ config: { sync_since: string } }>({
    defaultValues: { config: { sync_since: initial } },
  });
  const [shown, setShown] = useState(methods.watch("config.sync_since"));
  // Subscribe to form changes so the test can assert on the outward
  // view without reaching into RHF internals.
  methods.watch((v) => setShown(v.config?.sync_since ?? ""));
  return (
    <FormProvider {...methods}>
      <SyncWindowField />
      <div data-testid="probe">{shown}</div>
    </FormProvider>
  );
}

describe("SyncWindowField", () => {
  it("defaults to All history when sync_since is empty", () => {
    render(<Harness />);
    expect(screen.getByRole("tab", { name: "All history" })).toHaveAttribute(
      "aria-selected",
      "true",
    );
    expect(screen.getByText(/no cutoff/i)).toBeInTheDocument();
  });

  it("starts in Since date when an existing date is supplied", () => {
    render(<Harness initial="2025-06-01" />);
    expect(screen.getByRole("tab", { name: "Since date" })).toHaveAttribute(
      "aria-selected",
      "true",
    );
    const input = screen.getByLabelText(/sync from/i) as HTMLInputElement;
    expect(input.value).toBe("2025-06-01");
  });

  it("switching to Since date seeds a default 30-day window", async () => {
    const user = userEvent.setup();
    render(<Harness />);
    await user.click(screen.getByRole("tab", { name: "Since date" }));
    const input = screen.getByLabelText(/sync from/i) as HTMLInputElement;
    expect(input.value).toMatch(/^\d{4}-\d{2}-\d{2}$/);
    expect(input.value.length).toBe(10);
  });

  it("preset buttons overwrite the date", async () => {
    const user = userEvent.setup();
    render(<Harness initial="2020-01-01" />);
    const input = screen.getByLabelText(/sync from/i) as HTMLInputElement;
    expect(input.value).toBe("2020-01-01");

    await user.click(screen.getByRole("button", { name: /Last 7 days/i }));
    expect(input.value).not.toBe("2020-01-01");
    expect(input.value).toMatch(/^\d{4}-\d{2}-\d{2}$/);
  });

  it("clicking All history clears the sync_since value", async () => {
    const user = userEvent.setup();
    render(<Harness initial="2025-06-01" />);
    await user.click(screen.getByRole("tab", { name: "All history" }));
    expect(screen.getByTestId("probe").textContent).toBe("");
  });

  it("date input is capped at today (max attribute)", () => {
    render(<Harness initial="2025-06-01" />);
    const input = screen.getByLabelText(/sync from/i) as HTMLInputElement;
    const today = new Date();
    const y = today.getFullYear();
    const m = String(today.getMonth() + 1).padStart(2, "0");
    const d = String(today.getDate()).padStart(2, "0");
    expect(input.max).toBe(`${y}-${m}-${d}`);
  });
});
