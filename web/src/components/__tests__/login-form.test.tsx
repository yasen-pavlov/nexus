import { describe, it, expect } from "vitest";
import { http, HttpResponse } from "msw";
import { renderWithRouter, screen, userEvent, waitFor } from "@/test/test-utils";
import { LoginForm } from "../login-form";
import { server } from "@/test/mocks/server";

function renderLogin() {
  return renderWithRouter(<LoginForm />);
}

// Headline text that uniquely identifies sign-in vs register mode.
const SIGN_IN_HEADLINE = "Sign in";
const REGISTER_HEADLINE = "Welcome to your Nexus";
const REGISTER_BUTTON = "Create admin account";

describe("LoginForm", () => {
  it("renders the sign-in form by default", async () => {
    renderLogin();
    await waitFor(() => {
      expect(
        screen.getByRole("heading", { name: SIGN_IN_HEADLINE }),
      ).toBeInTheDocument();
    });
    expect(screen.getByLabelText("Username")).toBeInTheDocument();
    expect(screen.getByLabelText("Password")).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: SIGN_IN_HEADLINE }),
    ).toBeInTheDocument();
  });

  it("renders the register form when setup_required is true", async () => {
    server.use(
      http.get("*/api/health", () =>
        HttpResponse.json({ data: { status: "ok", setup_required: true } }),
      ),
    );
    renderLogin();
    await waitFor(() => {
      expect(screen.getByText(REGISTER_HEADLINE)).toBeInTheDocument();
    });
    expect(screen.getByText("First boot")).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: REGISTER_BUTTON }),
    ).toBeInTheDocument();
  });

  it("shows validation error for empty username", async () => {
    renderLogin();
    const user = userEvent.setup();
    await waitFor(() => {
      expect(
        screen.getByRole("heading", { name: SIGN_IN_HEADLINE }),
      ).toBeInTheDocument();
    });
    await user.click(screen.getByRole("button", { name: SIGN_IN_HEADLINE }));
    await waitFor(() => {
      expect(screen.getByText("Username is required")).toBeInTheDocument();
    });
  });

  it("shows validation error for empty password", async () => {
    renderLogin();
    const user = userEvent.setup();
    await waitFor(() => {
      expect(
        screen.getByRole("heading", { name: SIGN_IN_HEADLINE }),
      ).toBeInTheDocument();
    });
    await user.type(screen.getByLabelText("Username"), "admin");
    await user.click(screen.getByRole("button", { name: SIGN_IN_HEADLINE }));
    await waitFor(() => {
      expect(screen.getByText("Password is required")).toBeInTheDocument();
    });
  });

  it("shows validation error for short password in register mode", async () => {
    server.use(
      http.get("*/api/health", () =>
        HttpResponse.json({ data: { status: "ok", setup_required: true } }),
      ),
    );
    renderLogin();
    const user = userEvent.setup();
    await waitFor(() => {
      expect(screen.getByText(REGISTER_HEADLINE)).toBeInTheDocument();
    });
    await user.type(screen.getByLabelText("Username"), "admin");
    await user.type(screen.getByLabelText("Password"), "short");
    await user.click(screen.getByRole("button", { name: REGISTER_BUTTON }));
    await waitFor(() => {
      expect(
        screen.getByText("Password must be at least 8 characters"),
      ).toBeInTheDocument();
    });
  });

  it("shows error message on failed login", async () => {
    renderLogin();
    const user = userEvent.setup();
    await waitFor(() => {
      expect(
        screen.getByRole("heading", { name: SIGN_IN_HEADLINE }),
      ).toBeInTheDocument();
    });
    await user.type(screen.getByLabelText("Username"), "wrong");
    await user.type(screen.getByLabelText("Password"), "wrong");
    await user.click(screen.getByRole("button", { name: SIGN_IN_HEADLINE }));
    await waitFor(() => {
      expect(screen.getByText("invalid credentials")).toBeInTheDocument();
    });
  });

  it("stores token on successful login", async () => {
    renderLogin();
    const user = userEvent.setup();
    await waitFor(() => {
      expect(
        screen.getByRole("heading", { name: SIGN_IN_HEADLINE }),
      ).toBeInTheDocument();
    });
    await user.type(screen.getByLabelText("Username"), "admin");
    await user.type(screen.getByLabelText("Password"), "password123");
    await user.click(screen.getByRole("button", { name: SIGN_IN_HEADLINE }));
    await waitFor(() => {
      expect(localStorage.getItem("nexus_jwt")).toBe("fake-jwt-token");
    });
  });
});
