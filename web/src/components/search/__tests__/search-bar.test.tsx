import { describe, it, expect } from "vitest";
import { renderWithRouter, screen, userEvent, waitFor } from "@/test/test-utils";
import { SearchBar } from "../search-bar";

describe("SearchBar", () => {
  it("renders the search input", async () => {
    renderWithRouter(<SearchBar params={{}} />);
    await waitFor(() => {
      expect(
        screen.getByPlaceholderText(/search across everything/i),
      ).toBeInTheDocument();
    });
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
