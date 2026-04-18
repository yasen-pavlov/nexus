import { describe, expect, it } from "vitest";
import { useState } from "react";
import { fireEvent, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

import { ScheduleField } from "../schedule-field";
import { render as renderWithProviders } from "@/test/test-utils";

describe("ScheduleField", () => {
  function Harness({ initial = "" }: { initial?: string }) {
    const [value, setValue] = useState(initial);
    return <ScheduleField value={value} onChange={setValue} />;
  }

  it("starts in Off when value is empty", () => {
    renderWithProviders(<Harness />);
    const offTab = screen.getByRole("tab", { name: "Off" });
    expect(offTab).toHaveAttribute("aria-selected", "true");
    expect(screen.getByText(/not run this connector automatically/i)).toBeInTheDocument();
  });

  it("detects hourly preset from a 0 * * * * expression", () => {
    renderWithProviders(<Harness initial="0 * * * *" />);
    expect(screen.getByRole("tab", { name: "Hourly" })).toHaveAttribute("aria-selected", "true");
  });

  it("detects daily preset and shows the hour ruler", () => {
    renderWithProviders(<Harness initial="0 9 * * *" />);
    expect(screen.getByRole("tab", { name: "Daily" })).toHaveAttribute("aria-selected", "true");
    expect(screen.getByText("09:00")).toBeInTheDocument();
  });

  it("switching Off → Hourly activates the Hourly tab", async () => {
    const user = userEvent.setup();
    renderWithProviders(<Harness />);
    await user.click(screen.getByRole("tab", { name: "Hourly" }));
    expect(screen.getByRole("tab", { name: "Hourly" })).toHaveAttribute(
      "aria-selected",
      "true",
    );
    // cronstrue phrasing ("every hour") appears in both the body paragraph
    // and the status plaque — we just assert the tab is selected.
  });

  it("Weekly requires at least one day and seeds with Monday", () => {
    renderWithProviders(<Harness initial="0 9 * * 1" />);
    expect(screen.getByRole("tab", { name: "Weekly" })).toHaveAttribute("aria-selected", "true");
    const monday = screen.getByRole("button", { name: "Mon" });
    expect(monday).toHaveAttribute("aria-pressed", "true");
  });

  it("Weekly day strip ignores deselect that would leave zero days", async () => {
    const user = userEvent.setup();
    renderWithProviders(<Harness initial="0 9 * * 1" />);
    const monday = screen.getByRole("button", { name: "Mon" });
    await user.click(monday);
    // Still selected because we refuse the empty set.
    expect(monday).toHaveAttribute("aria-pressed", "true");
  });

  it("Custom preset accepts and displays a raw cron expression", async () => {
    const user = userEvent.setup();
    renderWithProviders(<Harness />);
    await user.click(screen.getByRole("tab", { name: "Custom" }));
    const input = screen.getByPlaceholderText("0 */4 * * *");
    fireEvent.change(input, { target: { value: "*/15 * * * *" } });
    expect(screen.getByText(/every 15 minutes/i)).toBeInTheDocument();
  });
});
