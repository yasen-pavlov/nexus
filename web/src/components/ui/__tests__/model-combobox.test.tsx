import { describe, expect, it, vi } from "vitest";
import { fireEvent } from "@testing-library/react";
import { render, screen, userEvent } from "@/test/test-utils";

import { ModelCombobox } from "../model-combobox";
import type { ModelOption } from "@/lib/model-catalog";

const opts: ModelOption[] = [
  { value: "voyage-4-large", label: "voyage-4-large", dimension: 1024, notes: "Recommended" },
  { value: "voyage-3", label: "voyage-3", dimension: 1024, notes: "v3 base" },
  { value: "voyage-3-lite", label: "voyage-3-lite", dimension: 512, notes: "Lower cost" },
];

describe("ModelCombobox", () => {
  it("shows every option in the pristine state when value matches input", async () => {
    const onChange = vi.fn();
    render(
      <ModelCombobox value="voyage-4-large" onChange={onChange} options={opts} />,
    );

    // Focus to open — the saved value still fills the input, so all options
    // surface (a discovery-friendly default rather than a one-row self-match).
    const input = screen.getByRole("combobox") as HTMLInputElement;
    fireEvent.focus(input);

    expect(screen.getByText("voyage-4-large")).toBeInTheDocument();
    expect(screen.getByText("voyage-3")).toBeInTheDocument();
    expect(screen.getByText("voyage-3-lite")).toBeInTheDocument();
  });

  it("filters options as the user types", async () => {
    const onChange = vi.fn();
    render(
      <ModelCombobox value="" onChange={onChange} options={opts} />,
    );
    const input = screen.getByRole("combobox") as HTMLInputElement;
    await userEvent.type(input, "lite");

    expect(screen.getByText("voyage-3-lite")).toBeInTheDocument();
    expect(screen.queryByText("voyage-4-large")).not.toBeInTheDocument();
  });

  it("commits a custom value when no option matches", async () => {
    const onChange = vi.fn();
    render(
      <ModelCombobox value="" onChange={onChange} options={opts} />,
    );
    const input = screen.getByRole("combobox") as HTMLInputElement;
    await userEvent.type(input, "my-custom-model");

    // The "Use custom model" row appears.
    expect(screen.getByText(/use custom model/i)).toBeInTheDocument();

    // Press Enter to commit.
    fireEvent.keyDown(input, { key: "Enter" });
    expect(onChange).toHaveBeenLastCalledWith("my-custom-model");
  });

  it("shows dimension chips by default and hides them with showDimensions={false}", () => {
    const { rerender } = render(
      <ModelCombobox value="voyage-4-large" onChange={() => {}} options={opts} />,
    );
    fireEvent.focus(screen.getByRole("combobox"));
    expect(screen.getAllByText("1024d").length).toBeGreaterThan(0);

    rerender(
      <ModelCombobox
        value="voyage-4-large"
        onChange={() => {}}
        options={opts}
        showDimensions={false}
      />,
    );
    expect(screen.queryByText("1024d")).not.toBeInTheDocument();
  });

  it("supports keyboard navigation with ArrowDown + Enter", async () => {
    const onChange = vi.fn();
    render(
      <ModelCombobox value="" onChange={onChange} options={opts} />,
    );
    const input = screen.getByRole("combobox") as HTMLInputElement;
    fireEvent.focus(input);

    fireEvent.keyDown(input, { key: "ArrowDown" });
    fireEvent.keyDown(input, { key: "ArrowDown" });
    fireEvent.keyDown(input, { key: "Enter" });

    // Two ArrowDowns from the initial highlight (0) lands on index 2 → voyage-3-lite.
    expect(onChange).toHaveBeenCalledWith("voyage-3-lite");
  });
});
