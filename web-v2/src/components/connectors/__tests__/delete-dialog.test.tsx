import { describe, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

import { DeleteConnectorDialog } from "../delete-dialog";
import { render as renderWithProviders } from "@/test/test-utils";

describe("DeleteConnectorDialog", () => {
  it("disables the confirm button until the name is typed exactly", async () => {
    const user = userEvent.setup();
    const onConfirm = vi.fn().mockResolvedValue(undefined);

    renderWithProviders(
      <DeleteConnectorDialog
        open
        onOpenChange={() => {}}
        connectorName="my-telegram"
        connectorType="telegram"
        onConfirm={onConfirm}
      />,
    );

    const confirm = screen.getByRole("button", { name: /remove my-telegram/i });
    expect(confirm).toBeDisabled();

    const input = screen.getByPlaceholderText("my-telegram");
    await user.type(input, "my-tel");
    expect(confirm).toBeDisabled();

    await user.type(input, "egram");
    expect(confirm).not.toBeDisabled();
  });

  it("shows impact copy specific to the connector type", () => {
    renderWithProviders(
      <DeleteConnectorDialog
        open
        onOpenChange={() => {}}
        connectorName="mailbox"
        connectorType="imap"
        onConfirm={() => Promise.resolve()}
      />,
    );
    expect(screen.getByText(/cached email bodies/i)).toBeInTheDocument();
  });

  it("fires onConfirm when armed and clicked", async () => {
    const user = userEvent.setup();
    const onConfirm = vi.fn().mockResolvedValue(undefined);

    renderWithProviders(
      <DeleteConnectorDialog
        open
        onOpenChange={() => {}}
        connectorName="notes"
        connectorType="filesystem"
        onConfirm={onConfirm}
      />,
    );

    await user.type(screen.getByPlaceholderText("notes"), "notes");
    await user.click(screen.getByRole("button", { name: /remove notes/i }));
    expect(onConfirm).toHaveBeenCalledTimes(1);
  });
});
