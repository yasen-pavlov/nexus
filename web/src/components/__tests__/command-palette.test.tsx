import { describe, it, expect, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

import { CommandPalette, type PaletteItem, PaletteIcon } from "../command-palette";
import { Search } from "lucide-react";

type FnSpy = ReturnType<typeof vi.fn<() => void>>;

function makeItems(spies: Record<string, FnSpy>): PaletteItem[] {
  return [
    {
      id: "page-search",
      group: "Pages",
      label: "Search",
      hint: "Find anything",
      icon: <PaletteIcon><Search /></PaletteIcon>,
      onSelect: spies.onSearch,
    },
    {
      id: "page-connectors",
      group: "Pages",
      label: "Connectors",
      hint: "Manage data sources",
      icon: <PaletteIcon><Search /></PaletteIcon>,
      onSelect: spies.onConnectors,
    },
    {
      id: "action-signout",
      group: "Actions",
      label: "Sign out",
      icon: <PaletteIcon tone="muted"><Search /></PaletteIcon>,
      onSelect: spies.onSignOut,
    },
  ];
}

describe("CommandPalette", () => {
  it("renders nothing when closed", () => {
    render(
      <CommandPalette
        open={false}
        onOpenChange={() => {}}
        items={makeItems({ onSearch: vi.fn(), onConnectors: vi.fn(), onSignOut: vi.fn() })}
      />,
    );
    expect(screen.queryByLabelText(/command palette/i)).not.toBeInTheDocument();
  });

  it("shows all items when open with no query", async () => {
    render(
      <CommandPalette
        open
        onOpenChange={() => {}}
        items={makeItems({ onSearch: vi.fn(), onConnectors: vi.fn(), onSignOut: vi.fn() })}
      />,
    );
    await waitFor(() => {
      expect(screen.getByText("Search")).toBeInTheDocument();
    });
    expect(screen.getByText("Connectors")).toBeInTheDocument();
    expect(screen.getByText("Sign out")).toBeInTheDocument();
    expect(screen.getByText("Pages")).toBeInTheDocument();
    expect(screen.getByText("Actions")).toBeInTheDocument();
  });

  it("filters items as the user types", async () => {
    const spies = { onSearch: vi.fn(), onConnectors: vi.fn(), onSignOut: vi.fn() };
    render(
      <CommandPalette
        open
        onOpenChange={() => {}}
        items={makeItems(spies)}
      />,
    );
    await waitFor(() => {
      expect(screen.getByPlaceholderText(/jump to or search/i)).toBeInTheDocument();
    });
    const input = screen.getByPlaceholderText(/jump to or search/i);
    await userEvent.type(input, "sign");
    expect(screen.getByText("Sign out")).toBeInTheDocument();
    expect(screen.queryByText("Search")).not.toBeInTheDocument();
    expect(screen.queryByText("Connectors")).not.toBeInTheDocument();
  });

  it("Enter activates the highlighted item and closes the palette", async () => {
    const spies = { onSearch: vi.fn(), onConnectors: vi.fn(), onSignOut: vi.fn() };
    const onOpenChange = vi.fn();
    render(
      <CommandPalette open onOpenChange={onOpenChange} items={makeItems(spies)} />,
    );
    await waitFor(() => {
      expect(screen.getByPlaceholderText(/jump to or search/i)).toBeInTheDocument();
    });
    await userEvent.keyboard("{Enter}");
    expect(spies.onSearch).toHaveBeenCalled();
    expect(onOpenChange).toHaveBeenCalledWith(false);
  });

  it("ArrowDown moves selection and Enter triggers the next item", async () => {
    const spies = { onSearch: vi.fn(), onConnectors: vi.fn(), onSignOut: vi.fn() };
    render(<CommandPalette open onOpenChange={() => {}} items={makeItems(spies)} />);
    await waitFor(() => {
      expect(screen.getByPlaceholderText(/jump to or search/i)).toBeInTheDocument();
    });
    await userEvent.keyboard("{ArrowDown}{Enter}");
    expect(spies.onConnectors).toHaveBeenCalled();
    expect(spies.onSearch).not.toHaveBeenCalled();
  });

  it("clicking an item activates it", async () => {
    const spies = { onSearch: vi.fn(), onConnectors: vi.fn(), onSignOut: vi.fn() };
    const onOpenChange = vi.fn();
    render(<CommandPalette open onOpenChange={onOpenChange} items={makeItems(spies)} />);
    await waitFor(() => {
      expect(screen.getByText("Connectors")).toBeInTheDocument();
    });
    await userEvent.click(screen.getByText("Connectors"));
    expect(spies.onConnectors).toHaveBeenCalled();
    expect(onOpenChange).toHaveBeenCalledWith(false);
  });

  it("shows an empty state when nothing matches", async () => {
    render(
      <CommandPalette
        open
        onOpenChange={() => {}}
        items={makeItems({ onSearch: vi.fn(), onConnectors: vi.fn(), onSignOut: vi.fn() })}
      />,
    );
    await waitFor(() => {
      expect(screen.getByPlaceholderText(/jump to or search/i)).toBeInTheDocument();
    });
    const input = screen.getByPlaceholderText(/jump to or search/i);
    await userEvent.type(input, "zzzzz-no-match");
    expect(screen.getByText(/nothing matches/i)).toBeInTheDocument();
  });
});
