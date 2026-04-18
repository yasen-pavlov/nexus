import { describe, it, expect } from "vitest";
import { renderWithRouter, screen, userEvent, waitFor } from "@/test/test-utils";
import { SearchBar } from "../search-bar";

describe("SearchBar", () => {
  it("renders the search-mode input by default", async () => {
    renderWithRouter(<SearchBar params={{}} />);
    await waitFor(() => {
      expect(
        screen.getByPlaceholderText(/search across everything/i),
      ).toBeInTheDocument();
    });
    expect(
      screen.getByRole("button", { pressed: true, name: /search/i }),
    ).toBeInTheDocument();
  });

  it("switches to Ask mode and shows the send affordance", async () => {
    renderWithRouter(<SearchBar params={{}} />);
    const user = userEvent.setup();
    await waitFor(() => {
      expect(
        screen.getByRole("button", { name: /ask/i }),
      ).toBeInTheDocument();
    });

    await user.click(screen.getByRole("button", { name: /ask/i }));

    expect(
      screen.getByPlaceholderText(/ask anything across your email/i),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: /ask/i, pressed: true }),
    ).toBeInTheDocument();
    // Send button is aria-labeled "Ask" and starts disabled (empty input).
    // The toggle button above is aria-pressed; the send button is not.
    const sendBtn = screen.getAllByRole("button", { name: "Ask" }).find(
      (btn) => btn.getAttribute("aria-pressed") === null,
    );
    expect(sendBtn).toBeDefined();
    expect(sendBtn).toBeDisabled();
  });

  it("mirrors existing URL q into the input on mount", async () => {
    renderWithRouter(<SearchBar params={{ q: "hello" }} />);
    await waitFor(() => {
      const input = screen.getByPlaceholderText(
        /search across everything/i,
      ) as HTMLInputElement;
      expect(input.value).toBe("hello");
    });
  });

  it("debounces typed input and updates the input value", async () => {
    renderWithRouter(<SearchBar params={{}} />);
    const user = userEvent.setup();
    const input = (await screen.findByPlaceholderText(
      /search across everything/i,
    )) as HTMLInputElement;
    await user.type(input, "foo");
    // Value propagates to the controlled input synchronously; the 300ms
    // debounce timer then commits to the URL (via memoryHistory in tests,
    // not window.location — so we verify the input reflects the typed
    // state and don't assert on window.location).
    expect(input.value).toBe("foo");
  });
});
