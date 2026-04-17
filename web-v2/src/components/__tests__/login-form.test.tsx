import { describe, it, expect } from "vitest";
import { http, HttpResponse } from "msw";
import { renderWithRouter, screen, userEvent, waitFor } from "@/test/test-utils";
import { LoginForm } from "../login-form";
import { server } from "@/test/mocks/server";

function renderLogin() {
  return renderWithRouter(<LoginForm />);
}

describe("LoginForm", () => {
  it("renders the sign-in form by default", async () => {
    renderLogin();
    await waitFor(() => {
      expect(screen.getByText("Sign in to continue")).toBeInTheDocument();
    });
    expect(screen.getByLabelText("Username")).toBeInTheDocument();
    expect(screen.getByLabelText("Password")).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Sign in" }),
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
      expect(
        screen.getByText("Create the first admin account"),
      ).toBeInTheDocument();
    });
    expect(
      screen.getByRole("button", { name: "Create admin account" }),
    ).toBeInTheDocument();
  });

  it("shows validation error for empty username", async () => {
    renderLogin();
    const user = userEvent.setup();
    await waitFor(() => {
      expect(screen.getByText("Sign in to continue")).toBeInTheDocument();
    });
    await user.click(screen.getByRole("button", { name: "Sign in" }));
    await waitFor(() => {
      expect(screen.getByText("Username is required")).toBeInTheDocument();
    });
  });

  it("shows validation error for empty password", async () => {
    renderLogin();
    const user = userEvent.setup();
    await waitFor(() => {
      expect(screen.getByText("Sign in to continue")).toBeInTheDocument();
    });
    await user.type(screen.getByLabelText("Username"), "admin");
    await user.click(screen.getByRole("button", { name: "Sign in" }));
    await waitFor(() => {
      expect(screen.getByText("Password is required")).toBeInTheDocument();
    });
  });

  it("shows validation error for short password in register mode", async () => {
    server.use(
      http.get("/api/health", () =>
        HttpResponse.json({ data: { status: "ok", setup_required: true } }),
      ),
    );
    renderLogin();
    const user = userEvent.setup();
    await waitFor(() => {
      expect(
        screen.getByText("Create the first admin account"),
      ).toBeInTheDocument();
    });
    await user.type(screen.getByLabelText("Username"), "admin");
    await user.type(screen.getByLabelText("Password"), "short");
    await user.click(
      screen.getByRole("button", { name: "Create admin account" }),
    );
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
      expect(screen.getByText("Sign in to continue")).toBeInTheDocument();
    });
    await user.type(screen.getByLabelText("Username"), "wrong");
    await user.type(screen.getByLabelText("Password"), "wrong");
    await user.click(screen.getByRole("button", { name: "Sign in" }));
    await waitFor(() => {
      expect(screen.getByText("invalid credentials")).toBeInTheDocument();
    });
  });

  it("stores token on successful login", async () => {
    renderLogin();
    const user = userEvent.setup();
    await waitFor(() => {
      expect(screen.getByText("Sign in to continue")).toBeInTheDocument();
    });
    await user.type(screen.getByLabelText("Username"), "admin");
    await user.type(screen.getByLabelText("Password"), "password123");
    await user.click(screen.getByRole("button", { name: "Sign in" }));
    await waitFor(() => {
      expect(localStorage.getItem("nexus_jwt")).toBe("fake-jwt-token");
    });
  });
});
