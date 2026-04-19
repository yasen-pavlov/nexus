import { describe, expect, it } from "vitest";
import { Brain } from "lucide-react";

import { SettingsSection } from "../settings-section";
import { render, screen } from "@/test/test-utils";

describe("SettingsSection", () => {
  it("renders label, title, description and children inside the card", () => {
    render(
      <SettingsSection
        id="test"
        label="Engine · 01"
        title="Embeddings"
        description="Semantic search reads documents through this model."
        icon={Brain}
      >
        <div data-testid="body">hello</div>
      </SettingsSection>,
    );

    // Eyebrow label renders with section typography
    expect(screen.getByText("Engine · 01")).toBeInTheDocument();
    // Title + description
    expect(screen.getByText("Embeddings")).toBeInTheDocument();
    expect(
      screen.getByText(/Semantic search reads documents/),
    ).toBeInTheDocument();
    // Children materialize
    expect(screen.getByTestId("body")).toHaveTextContent("hello");
  });

  it("renders the actions slot next to the title", () => {
    render(
      <SettingsSection
        id="test"
        label="Engine · 01"
        title="Embeddings"
        actions={<button type="button">New</button>}
      >
        <div>body</div>
      </SettingsSection>,
    );
    expect(screen.getByRole("button", { name: /new/i })).toBeInTheDocument();
  });

  it("anchors the section via id", () => {
    const { container } = render(
      <SettingsSection id="anchor-me" label="x" title="x">
        <div>body</div>
      </SettingsSection>,
    );
    expect(container.querySelector("#anchor-me")).toBeInTheDocument();
  });

  it("wraps children in a bordered card by default, and skips it when bare", () => {
    const wrapped = render(
      <SettingsSection id="a" label="x" title="x">
        <div data-testid="child">body</div>
      </SettingsSection>,
    );
    // The child's direct parent should have the card bg class.
    const wrappedChild = wrapped.getByTestId("child");
    expect(wrappedChild.parentElement?.className).toMatch(/bg-card/);
    wrapped.unmount();

    const bare = render(
      <SettingsSection id="b" label="x" title="x" bare>
        <div data-testid="child">body</div>
      </SettingsSection>,
    );
    const bareChild = bare.getByTestId("child");
    expect(bareChild.parentElement?.className ?? "").not.toMatch(/bg-card/);
  });
});
