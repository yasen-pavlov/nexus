import { describe, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

import { TelegramAuthPanel } from "../telegram-auth-panel";
import { render } from "@/test/test-utils";

function setup(overrides: Partial<React.ComponentProps<typeof TelegramAuthPanel>> = {}) {
  const onStart = vi.fn().mockResolvedValue(undefined);
  const onSubmit = vi.fn().mockResolvedValue(undefined);
  render(
    <TelegramAuthPanel
      phone="+15551234567"
      onStart={onStart}
      onSubmit={onSubmit}
      status="idle"
      {...overrides}
    />,
  );
  return { onStart, onSubmit };
}

describe("TelegramAuthPanel", () => {
  it("initially shows only the Send code button", () => {
    setup();
    expect(
      screen.getByRole("button", { name: /send code to telegram/i }),
    ).toBeInTheDocument();
    expect(screen.queryByLabelText(/login code/i)).not.toBeInTheDocument();
  });

  it("disables Send code when phone is empty", () => {
    setup({ phone: "" });
    expect(
      screen.getByRole("button", { name: /send code to telegram/i }),
    ).toBeDisabled();
  });

  it("calls onStart when Send code is clicked", async () => {
    const user = userEvent.setup();
    const { onStart } = setup();
    await user.click(
      screen.getByRole("button", { name: /send code to telegram/i }),
    );
    expect(onStart).toHaveBeenCalledOnce();
  });

  it("swaps to the verify form once status transitions to code-sent", () => {
    setup({ status: "code-sent" });
    expect(screen.getByLabelText(/login code/i)).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: /verify and connect/i }),
    ).toBeInTheDocument();
  });

  it("strips non-digit input from the code field", async () => {
    const user = userEvent.setup();
    setup({ status: "code-sent" });
    const code = screen.getByLabelText(/login code/i) as HTMLInputElement;
    await user.type(code, "12ab34");
    expect(code.value).toBe("1234");
  });

  it("reveals the 2FA password field when the backend asks for it", () => {
    setup({ status: "code-sent", needs2FA: true });
    expect(
      screen.getByLabelText(/two-step verification password/i),
    ).toBeInTheDocument();
  });

  it("shows the error banner when status=failed with an error", () => {
    setup({ status: "failed", error: "invalid code" });
    expect(screen.getByText("invalid code")).toBeInTheDocument();
  });

  it("Resend code button re-calls onStart", async () => {
    const user = userEvent.setup();
    const { onStart } = setup({ status: "code-sent" });
    await user.click(screen.getByRole("button", { name: /resend code/i }));
    expect(onStart).toHaveBeenCalledOnce();
  });

  it("Verify button stays disabled until the user enters a code", async () => {
    const user = userEvent.setup();
    setup({ status: "code-sent" });
    const verify = screen.getByRole("button", { name: /verify and connect/i });
    expect(verify).toBeDisabled();
    await user.type(screen.getByLabelText(/login code/i), "12345");
    await waitFor(() => expect(verify).not.toBeDisabled());
  });

  it("submits code + optional password when the form is filled", async () => {
    const user = userEvent.setup();
    const { onSubmit } = setup({ status: "code-sent", needs2FA: true });
    await user.type(screen.getByLabelText(/login code/i), "12345");
    await user.type(
      screen.getByLabelText(/two-step verification password/i),
      "hunter2",
    );
    await user.click(screen.getByRole("button", { name: /verify and connect/i }));
    expect(onSubmit).toHaveBeenCalledWith({ code: "12345", password: "hunter2" });
  });
});
