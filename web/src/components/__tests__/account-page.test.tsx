import { describe, it, expect } from "vitest";
import { ThemeProvider } from "next-themes";
import { renderWithRouter, screen, userEvent, waitFor } from "@/test/test-utils";
import { setToken } from "@/lib/api-client";
import { fakeToken } from "@/test/mocks/handlers";

import { AccountPage } from "../account/account-page";
import type { User } from "@/lib/api-types";

const fakeUser: User = {
  id: "u-100",
  username: "alice",
  role: "admin",
  created_at: "2026-03-01T10:00:00Z",
};

function Wrapped({ user }: { user: User }) {
  return (
    <ThemeProvider attribute="class" defaultTheme="light" enableSystem>
      <AccountPage user={user} />
    </ThemeProvider>
  );
}

describe("AccountPage", () => {
  it("renders username + admin badge + member-since", async () => {
    setToken(fakeToken);
    renderWithRouter(<Wrapped user={fakeUser} />);
    expect(await screen.findByText("alice")).toBeInTheDocument();
    expect(screen.getByText(/admin/i)).toBeInTheDocument();
    // The member-since uses formatRelative which produces "X ago" — assert
    // the static prefix that doesn't depend on the wall clock.
    expect(screen.getByText(/member/i)).toBeInTheDocument();
  });

  it("opens the change-password sheet when the button is clicked", async () => {
    setToken(fakeToken);
    renderWithRouter(<Wrapped user={fakeUser} />);
    const change = await screen.findByRole("button", { name: /change…/i });
    await userEvent.click(change);
    await waitFor(() => {
      expect(screen.getByText(/set a new password/i)).toBeInTheDocument();
    });
  });

  it("renders a regular-user badge for non-admin", async () => {
    setToken(fakeToken);
    renderWithRouter(
      <Wrapped user={{ ...fakeUser, username: "bob", role: "user" }} />,
    );
    expect(await screen.findByText("bob")).toBeInTheDocument();
    // Admin pill has both "admin" text and ShieldCheck — for "user" we
    // assert the role text appears.
    expect(screen.getByText(/^user$/i)).toBeInTheDocument();
  });
});
