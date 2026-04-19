import { describe, it, expect } from "vitest";
import { renderWithRouter, screen, userEvent, waitFor } from "@/test/test-utils";

import { ErrorPage } from "../error-page";

describe("ErrorPage", () => {
  it("renders 404 copy with Go home + Try search", async () => {
    renderWithRouter(<ErrorPage kind="404" />);
    await waitFor(() => {
      expect(
        screen.getByText(/we couldn't find that page/i),
      ).toBeInTheDocument();
    });
    expect(screen.getByText(/404/i)).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /go home/i })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /try search/i })).toBeInTheDocument();
  });

  it("renders error copy with Reload button", async () => {
    const err = new Error("boom");
    renderWithRouter(<ErrorPage kind="error" error={err} />);
    await waitFor(() => {
      expect(screen.getByText(/this page hit a snag/i)).toBeInTheDocument();
    });
    expect(screen.getByText(/something went wrong/i)).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: /reload page/i }),
    ).toBeInTheDocument();
  });

  it("technical details disclosure is collapsed by default and toggles on click", async () => {
    const err = new Error("kaboom-specific-message");
    renderWithRouter(<ErrorPage kind="error" error={err} />);

    const toggle = await screen.findByRole("button", {
      name: /technical details/i,
    });
    expect(screen.queryByText(/kaboom-specific-message/i)).not.toBeInTheDocument();
    expect(toggle).toHaveAttribute("aria-expanded", "false");
    await userEvent.click(toggle);
    expect(toggle).toHaveAttribute("aria-expanded", "true");
    expect(screen.getByText(/kaboom-specific-message/i)).toBeInTheDocument();
  });

  it("404 variant does not render technical details", async () => {
    renderWithRouter(<ErrorPage kind="404" />);
    // Wait for router boot first so the empty-DOM phase doesn't false-pass
    // the absence assertion.
    await screen.findByText(/we couldn't find that page/i);
    expect(
      screen.queryByRole("button", { name: /technical details/i }),
    ).not.toBeInTheDocument();
  });
});
