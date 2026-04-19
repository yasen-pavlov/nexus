import { describe, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

import { ConnectorForm, type ConnectorFormValues } from "../connector-form";
import { render } from "@/test/test-utils";

function setup(overrides: Partial<React.ComponentProps<typeof ConnectorForm>> = {}) {
  const onSubmit = vi.fn().mockResolvedValue(undefined);
  const onCancel = vi.fn();
  render(
    <ConnectorForm
      mode="create"
      onSubmit={onSubmit}
      onCancel={onCancel}
      isAdmin
      {...overrides}
    />,
  );
  return { onSubmit, onCancel };
}

describe("ConnectorForm — create", () => {
  it("defaults to the filesystem type with its field group", () => {
    setup();
    expect(screen.getByRole("button", { name: /selected/i })).toBeInTheDocument();
    // Filesystem's root-path input rendered by default.
    expect(screen.getByLabelText(/root path/i)).toBeInTheDocument();
  });

  it("switching the type swaps the field group", async () => {
    const user = userEvent.setup();
    setup();
    await user.click(screen.getByRole("button", { name: /telegram/i }));
    await waitFor(() => {
      expect(screen.getByLabelText(/api hash/i)).toBeInTheDocument();
    });
    // Root path is gone — filesystem group unmounted.
    expect(screen.queryByLabelText(/root path/i)).not.toBeInTheDocument();
  });

  it("surfaces a validation error for an empty root path on filesystem", async () => {
    const user = userEvent.setup();
    const { onSubmit } = setup();
    // Blow away the pre-seeded name so required checks fire. Leave
    // root_path empty to hit the zod refinement.
    const nameInput = screen.getByLabelText("Name") as HTMLInputElement;
    await user.clear(nameInput);
    await user.type(nameInput, "notes");
    await user.click(screen.getByRole("button", { name: /create connector/i }));
    await waitFor(() => {
      expect(screen.getByText(/root path is required/i)).toBeInTheDocument();
    });
    expect(onSubmit).not.toHaveBeenCalled();
  });

  it("submits a valid filesystem config when the form is complete", async () => {
    const user = userEvent.setup();
    const { onSubmit } = setup();
    const root = screen.getByLabelText(/root path/i);
    await user.type(root, "/home/you/notes");
    await user.click(screen.getByRole("button", { name: /create connector/i }));
    await waitFor(() => {
      expect(onSubmit).toHaveBeenCalledOnce();
    });
    const values = onSubmit.mock.calls[0][0] as ConnectorFormValues;
    expect(values.type).toBe("filesystem");
    if (values.type === "filesystem") {
      expect(values.config.root_path).toBe("/home/you/notes");
    }
  });

  it("shows 'Share with all users' only when isAdmin is true", async () => {
    setup({ isAdmin: false });
    expect(screen.queryByText(/share with all users/i)).not.toBeInTheDocument();
  });

  it("flags an invalid Telegram phone format", async () => {
    const user = userEvent.setup();
    const { onSubmit } = setup();
    await user.click(screen.getByRole("button", { name: /telegram/i }));
    await waitFor(() => {
      expect(screen.getByLabelText(/phone/i)).toBeInTheDocument();
    });
    await user.type(screen.getByLabelText(/api id/i), "12345");
    await user.type(screen.getByLabelText(/api hash/i), "deadbeefdeadbeef");
    await user.type(screen.getByLabelText(/phone/i), "5551234");
    await user.click(screen.getByRole("button", { name: /create connector/i }));
    await waitFor(() => {
      expect(
        screen.getByText(/international format/i),
      ).toBeInTheDocument();
    });
    expect(onSubmit).not.toHaveBeenCalled();
  });
});

describe("ConnectorForm — edit", () => {
  it("hides the type picker and shows masked placeholder on IMAP password", () => {
    render(
      <ConnectorForm
        mode="edit"
        initial={{
          type: "imap",
          name: "personal-email",
          enabled: true,
          shared: false,
          schedule: "",
          config: {
            server: "imap.fastmail.com",
            port: 993,
            username: "me@example.com",
            password: "****abcd",
            folders: "INBOX",
          },
        }}
        onSubmit={vi.fn()}
        onCancel={vi.fn()}
      />,
    );
    // The type-picker tiles aren't rendered in edit mode.
    expect(
      screen.queryByRole("button", { name: /telegram/i }),
    ).not.toBeInTheDocument();
    // The IMAP password uses the masked placeholder when mode=edit.
    const pw = screen.getByLabelText(/password/i) as HTMLInputElement;
    expect(pw.placeholder).toMatch(/••••••••/);
  });
});
