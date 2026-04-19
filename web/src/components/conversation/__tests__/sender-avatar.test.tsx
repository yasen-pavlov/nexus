import { describe, it, expect } from "vitest";
import { render, screen } from "@/test/test-utils";
import { SenderAvatar, initialsFor, hueFor } from "../sender-avatar";

describe("initialsFor", () => {
  it("returns first+last initial for a two-word name", () => {
    expect(initialsFor("Alice Kim", "seed")).toBe("AK");
  });

  it("returns the first two chars for a single-word name", () => {
    expect(initialsFor("Alice", "seed")).toBe("AL");
  });

  it("falls back to the seed when name is empty", () => {
    expect(initialsFor("", "12345")).toBe("12");
    expect(initialsFor(undefined, "x")).toBe("X");
  });
});

describe("hueFor", () => {
  it("returns a stable hue for the same seed", () => {
    expect(hueFor("same")).toBe(hueFor("same"));
  });
});

describe("SenderAvatar", () => {
  it("renders initials when no blob url is provided", () => {
    render(<SenderAvatar senderName="Alice Kim" seed="1" />);
    expect(screen.getByRole("img", { name: "Alice Kim" })).toHaveTextContent(
      "AK",
    );
  });

  it("renders an <img> when a blob url is provided", () => {
    render(
      <SenderAvatar
        blobUrl="blob:xyz"
        senderName="Alice Kim"
        seed="1"
      />,
    );
    const img = screen.getByRole("img", { name: "Alice Kim" });
    expect(img.tagName).toBe("IMG");
    expect(img).toHaveAttribute("src", "blob:xyz");
  });
});
