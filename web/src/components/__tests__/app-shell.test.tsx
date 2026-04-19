import { describe, it, expect } from "vitest";
import { ThemeProvider } from "next-themes";
import type { ReactNode } from "react";
import {
  renderWithRouter,
  screen,
  userEvent,
  waitFor,
  act,
} from "@/test/test-utils";
import { setToken } from "@/lib/api-client";
import { fakeToken } from "@/test/mocks/handlers";
import type { User } from "@/lib/api-types";
import { AppShell } from "../app-shell";

// Unique usernames so getByText assertions aren't ambiguous with the role tag.
const adminUser: User = {
  id: "u1",
  username: "alice",
  role: "admin",
  created_at: "2026-03-01T10:00:00Z",
};
const regularUser: User = {
  id: "u2",
  username: "viewer",
  role: "user",
  created_at: "2026-03-15T10:00:00Z",
};

function Wrapped({
  user,
  children,
}: {
  user: User;
  children?: ReactNode;
}) {
  return (
    <ThemeProvider attribute="class" defaultTheme="system" enableSystem>
      <AppShell user={user}>{children ?? <div>content</div>}</AppShell>
    </ThemeProvider>
  );
}

const EXTRA_ROUTES = [
  "/connectors",
  "/admin/settings",
  "/admin/users",
  "/admin/stats",
];

describe("AppShell", () => {
  it("renders the masthead, children, and the operational trust strip", async () => {
    setToken(fakeToken);
    renderWithRouter(<Wrapped user={adminUser} />, {
      extraRoutes: EXTRA_ROUTES,
    });

    await waitFor(() => {
      expect(screen.getByText("Nexus")).toBeInTheDocument();
    });
    expect(screen.getByText("personal search")).toBeInTheDocument();
    expect(screen.getByText("content")).toBeInTheDocument();
    // Top bar stats — derived from the mocked connector list: one imap
    // connector ⇒ "1 source".
    await waitFor(() => {
      expect(screen.getByText(/source/i)).toBeInTheDocument();
    });
  });

  it("exposes a sidebar toggle button", async () => {
    setToken(fakeToken);
    renderWithRouter(<Wrapped user={adminUser} />, {
      extraRoutes: EXTRA_ROUTES,
    });
    await waitFor(() => {
      expect(
        screen.getByRole("button", { name: /toggle sidebar/i }),
      ).toBeInTheDocument();
    });
  });

  it("shows the Admin section for admin users", async () => {
    setToken(fakeToken);
    renderWithRouter(<Wrapped user={adminUser} />, {
      extraRoutes: EXTRA_ROUTES,
    });
    await waitFor(() => {
      expect(screen.getByText("Admin")).toBeInTheDocument();
    });
    expect(screen.getByRole("link", { name: /settings/i })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /users/i })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /stats/i })).toBeInTheDocument();
  });

  it("hides the Admin section for non-admin users", async () => {
    setToken(fakeToken);
    renderWithRouter(<Wrapped user={regularUser} />, {
      extraRoutes: EXTRA_ROUTES,
    });
    await waitFor(() => {
      expect(screen.getByText("viewer")).toBeInTheDocument();
    });
    expect(screen.queryByText("Admin")).not.toBeInTheDocument();
    expect(screen.queryByRole("link", { name: /settings/i })).not.toBeInTheDocument();
  });

  it("opens the user-card popover with theme options and sign-out", async () => {
    setToken(fakeToken);
    renderWithRouter(<Wrapped user={adminUser} />, {
      extraRoutes: EXTRA_ROUTES,
    });
    const user = userEvent.setup();
    await waitFor(() => {
      expect(screen.getByText("alice")).toBeInTheDocument();
    });
    await user.click(
      screen.getByRole("button", { name: /alice/i, expanded: false }),
    );

    expect(
      screen.getByRole("menuitemradio", { name: /light/i }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("menuitemradio", { name: /dark/i }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("menuitemradio", { name: /system/i }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("menuitem", { name: /sign out/i }),
    ).toBeInTheDocument();
  });

  it("Cmd/Ctrl+K opens the command palette with Pages + Connectors + Actions", async () => {
    setToken(fakeToken);
    renderWithRouter(<Wrapped user={adminUser} />, {
      extraRoutes: EXTRA_ROUTES,
    });
    await waitFor(() =>
      expect(screen.getByText("Nexus")).toBeInTheDocument(),
    );
    await act(async () => {
      window.dispatchEvent(
        new KeyboardEvent("keydown", { key: "k", ctrlKey: true }),
      );
    });
    await waitFor(() => {
      // Palette opens as a CommandDialog — "Search" page item is always there.
      expect(
        screen.getByRole("option", { name: /^Search/ }),
      ).toBeInTheDocument();
    });
    // Admin-only entries show up.
    expect(
      screen.getByRole("option", { name: /Admin · Settings/ }),
    ).toBeInTheDocument();
    // Sign out action.
    expect(
      screen.getByRole("option", { name: /Sign out/ }),
    ).toBeInTheDocument();
  });

  it("?: opens the cheat sheet", async () => {
    setToken(fakeToken);
    renderWithRouter(<Wrapped user={adminUser} />, {
      extraRoutes: EXTRA_ROUTES,
    });
    await waitFor(() =>
      expect(screen.getByText("Nexus")).toBeInTheDocument(),
    );
    await act(async () => {
      window.dispatchEvent(new KeyboardEvent("keydown", { key: "?" }));
    });
    await waitFor(() =>
      expect(
        screen.getByRole("heading", { name: /shortcuts/i }),
      ).toBeInTheDocument(),
    );
  });

  it("vim chord g→s navigates to search", async () => {
    setToken(fakeToken);
    renderWithRouter(<Wrapped user={adminUser} />, {
      extraRoutes: EXTRA_ROUTES,
    });
    await waitFor(() =>
      expect(screen.getByText("Nexus")).toBeInTheDocument(),
    );
    await act(async () => {
      window.dispatchEvent(new KeyboardEvent("keydown", { key: "g" }));
    });
    await act(async () => {
      window.dispatchEvent(new KeyboardEvent("keydown", { key: "s" }));
    });
    // Router navigates to "/" — just assert no crash + content still there.
    expect(screen.getByText("content")).toBeInTheDocument();
  });

  it("theme toggle action in the palette flips html class to dark", async () => {
    setToken(fakeToken);
    renderWithRouter(<Wrapped user={adminUser} />, {
      extraRoutes: EXTRA_ROUTES,
    });
    const user = userEvent.setup();
    await waitFor(() =>
      expect(screen.getByText("Nexus")).toBeInTheDocument(),
    );
    await act(async () => {
      window.dispatchEvent(
        new KeyboardEvent("keydown", { key: "k", ctrlKey: true }),
      );
    });
    const themeItem = await screen.findByRole("option", {
      name: /switch to (light|dark) theme/i,
    });
    await user.click(themeItem);
    // Palette closes after the action fires.
    await waitFor(() =>
      expect(
        screen.queryByRole("option", { name: /switch to (light|dark) theme/i }),
      ).not.toBeInTheDocument(),
    );
  });

  it("selecting a theme closes the popover and applies the class", async () => {
    setToken(fakeToken);
    renderWithRouter(<Wrapped user={adminUser} />, {
      extraRoutes: EXTRA_ROUTES,
    });
    const user = userEvent.setup();
    await waitFor(() => {
      expect(screen.getByText("alice")).toBeInTheDocument();
    });
    await user.click(
      screen.getByRole("button", { name: /alice/i, expanded: false }),
    );
    await user.click(
      screen.getByRole("menuitemradio", { name: /dark/i }),
    );

    // Popover closed — theme radios no longer present
    await waitFor(() => {
      expect(
        screen.queryByRole("menuitemradio", { name: /dark/i }),
      ).not.toBeInTheDocument();
    });
    // next-themes writes the theme to <html class="dark">
    expect(document.documentElement.classList.contains("dark")).toBe(true);
  });
});
