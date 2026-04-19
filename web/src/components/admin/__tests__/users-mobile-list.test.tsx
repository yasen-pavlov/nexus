import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

import { UsersMobileList } from "../users-mobile-list";
import type { AdminUserRow } from "@/hooks/use-users";

const rows: AdminUserRow[] = [
  {
    id: "u-1",
    username: "alice",
    role: "admin",
    created_at: "2026-01-01T00:00:00Z",
  },
  {
    id: "u-2",
    username: "bob",
    role: "user",
    created_at: "2026-02-15T00:00:00Z",
  },
];

describe("UsersMobileList", () => {
  it("renders one card per row with username + role + since", () => {
    render(
      <UsersMobileList
        rows={rows}
        currentUserId="u-1"
        onChangePassword={() => {}}
        onDelete={() => {}}
      />,
    );
    expect(screen.getByText("alice")).toBeInTheDocument();
    expect(screen.getByText("bob")).toBeInTheDocument();
    expect(screen.getByText(/admin/i)).toBeInTheDocument();
    expect(screen.getAllByText(/since/i).length).toBeGreaterThan(0);
  });

  it("shows the 'you' badge on the current user's card", () => {
    render(
      <UsersMobileList
        rows={rows}
        currentUserId="u-1"
        onChangePassword={() => {}}
        onDelete={() => {}}
      />,
    );
    expect(screen.getByText(/^you$/i)).toBeInTheDocument();
  });

  it("calls onChangePassword when the dropdown action is selected", async () => {
    const onChangePassword = vi.fn();
    render(
      <UsersMobileList
        rows={rows}
        currentUserId="u-1"
        onChangePassword={onChangePassword}
        onDelete={() => {}}
      />,
    );
    const triggers = screen.getAllByRole("button", {
      name: /actions for/i,
    });
    await userEvent.click(triggers[1]); // bob's actions
    const change = await screen.findByText(/change password/i);
    await userEvent.click(change);
    expect(onChangePassword).toHaveBeenCalledWith(
      expect.objectContaining({ id: "u-2", username: "bob" }),
    );
  });
});
