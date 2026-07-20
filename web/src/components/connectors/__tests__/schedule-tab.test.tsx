import { describe, expect, it, vi } from "vitest";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { toast } from "sonner";

import { ScheduleTab } from "../schedule-tab";
import { render as renderWithProviders } from "@/test/test-utils";

vi.mock("sonner", () => ({ toast: { error: vi.fn(), success: vi.fn() } }));

describe("ScheduleTab", () => {
  it("does NOT save on every ScheduleField change (no PUT per keystroke)", async () => {
    const onSave = vi.fn().mockResolvedValue(undefined);
    renderWithProviders(
      <ScheduleTab schedule="" canManage onSave={onSave} onClearCursor={() => {}} />,
    );
    await userEvent.setup().click(screen.getByRole("tab", { name: "Custom" }));
    fireEvent.change(screen.getByPlaceholderText("0 */4 * * *"), {
      target: { value: "0 5 * * 1" },
    });
    expect(onSave).not.toHaveBeenCalled();
  });

  it("saves the final draft exactly once on explicit Save", async () => {
    const user = userEvent.setup();
    const onSave = vi.fn().mockResolvedValue(undefined);
    renderWithProviders(
      <ScheduleTab schedule="" canManage onSave={onSave} onClearCursor={() => {}} />,
    );
    await user.click(screen.getByRole("tab", { name: "Hourly" }));
    await user.click(screen.getByRole("button", { name: /save schedule/i }));
    expect(onSave).toHaveBeenCalledTimes(1);
    expect(onSave).toHaveBeenCalledWith("0 * * * *");
    await waitFor(() =>
      expect(toast.success).toHaveBeenCalledWith("Schedule updated."),
    );
  });

  it("disables Save when the draft equals the saved schedule", () => {
    renderWithProviders(
      <ScheduleTab
        schedule="0 * * * *"
        canManage
        onSave={vi.fn()}
        onClearCursor={() => {}}
      />,
    );
    expect(screen.getByRole("button", { name: /save schedule/i })).toBeDisabled();
  });

  it("keeps Save disabled for a read-only user even after editing", async () => {
    const user = userEvent.setup();
    renderWithProviders(
      <ScheduleTab
        schedule=""
        canManage={false}
        onSave={vi.fn()}
        onClearCursor={() => {}}
      />,
    );
    await user.click(screen.getByRole("tab", { name: "Hourly" }));
    expect(screen.getByRole("button", { name: /save schedule/i })).toBeDisabled();
  });

  it("shows an error toast when the save rejects (e.g. a 400 from the backend)", async () => {
    vi.mocked(toast.error).mockClear();
    const user = userEvent.setup();
    const onSave = vi.fn().mockRejectedValue(new Error("invalid cron"));
    renderWithProviders(
      <ScheduleTab schedule="" canManage onSave={onSave} onClearCursor={() => {}} />,
    );
    await user.click(screen.getByRole("tab", { name: "Hourly" }));
    await user.click(screen.getByRole("button", { name: /save schedule/i }));
    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith("Couldn't update schedule."),
    );
  });
});
