import { describe, it, expect, vi } from "vitest";
import { render, screen, userEvent } from "@/test/test-utils";
import { ReplyQuote } from "../reply-quote";

describe("ReplyQuote", () => {
  it("renders a loading skeleton when state.status is loading", () => {
    const { container } = render(<ReplyQuote state={{ status: "loading" }} />);
    expect(container.querySelectorAll(".animate-pulse").length).toBeGreaterThan(
      0,
    );
  });

  it("renders 'unavailable' when state.status is unavailable", () => {
    render(<ReplyQuote state={{ status: "unavailable" }} />);
    expect(screen.getByText("unavailable")).toBeInTheDocument();
  });

  it("renders a clickable button when in-range and onJump is provided", async () => {
    const onJump = vi.fn();
    render(
      <ReplyQuote
        state={{
          status: "loaded",
          authorName: "Alice",
          snippet: "hello world",
          inRange: true,
          onJump,
        }}
      />,
    );
    const btn = screen.getByRole("button");
    await userEvent.click(btn);
    expect(onJump).toHaveBeenCalledOnce();
    expect(screen.getByText("Alice")).toBeInTheDocument();
    expect(screen.getByText("hello world")).toBeInTheDocument();
  });

  it("renders a non-clickable quote when out-of-range", () => {
    render(
      <ReplyQuote
        state={{
          status: "loaded",
          authorName: "Alice",
          snippet: "hello",
          inRange: false,
        }}
      />,
    );
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
  });
});
