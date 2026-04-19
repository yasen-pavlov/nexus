import { describe, expect, it, vi } from "vitest";

import { TypedConfirmDialog } from "../typed-confirm-dialog";
import { render, screen, userEvent, waitFor } from "@/test/test-utils";

describe("TypedConfirmDialog", () => {
  it("disables the destructive CTA until the phrase is typed exactly", async () => {
    const onConfirm = vi.fn();
    render(
      <TypedConfirmDialog
        open
        onOpenChange={() => {}}
        title="Remove account?"
        body={<span>Body copy</span>}
        confirmPhrase="alice"
        confirmLabel="Remove alice"
        onConfirm={onConfirm}
      />,
    );

    const cta = await screen.findByRole("button", { name: /remove alice/i });
    expect(cta).toBeDisabled();

    const input = screen.getByPlaceholderText("alice");
    await userEvent.type(input, "alic");
    expect(cta).toBeDisabled();

    await userEvent.type(input, "e");
    await waitFor(() => expect(cta).toBeEnabled());

    await userEvent.click(cta);
    expect(onConfirm).toHaveBeenCalled();
  });

  it("hides the Telegram-specific warning unless passed in the body", async () => {
    render(
      <TypedConfirmDialog
        open
        onOpenChange={() => {}}
        title="Wipe cache?"
        body={<span>Plain body</span>}
        confirmPhrase="foo"
        confirmLabel="Wipe"
        onConfirm={vi.fn()}
      />,
    );
    expect(screen.queryByText(/Telegram eager-cached/)).not.toBeInTheDocument();
  });
});
