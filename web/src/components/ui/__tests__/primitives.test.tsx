import { describe, expect, it, vi } from "vitest";
import * as React from "react";
import { fireEvent } from "@testing-library/react";
import { render, screen, userEvent, waitFor } from "@/test/test-utils";

import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogOverlay,
  DialogPortal,
  DialogTitle,
  DialogTrigger,
} from "../dialog";
import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuPortal,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuShortcut,
  DropdownMenuSub,
  DropdownMenuSubContent,
  DropdownMenuSubTrigger,
  DropdownMenuTrigger,
} from "../dropdown-menu";
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "../tooltip";
import { Slider } from "../slider";
import { Switch } from "../switch";

// Base-UI primitives — these are thin wrappers whose entire job is to pass
// props through and apply classes. A single render per primitive is enough
// to exercise every function definition.

describe("Dialog primitives", () => {
  it("renders trigger + content when opened", async () => {
    render(
      <Dialog>
        <DialogTrigger>open</DialogTrigger>
        <DialogPortal>
          <DialogOverlay />
          <DialogContent>
            <DialogHeader>
              <DialogTitle>title</DialogTitle>
              <DialogDescription>desc</DialogDescription>
            </DialogHeader>
            <DialogFooter>
              <DialogClose>close</DialogClose>
            </DialogFooter>
          </DialogContent>
        </DialogPortal>
      </Dialog>,
    );
    await userEvent.click(screen.getByText("open"));
    await waitFor(() => expect(screen.getByText("title")).toBeInTheDocument());
    expect(screen.getByText("desc")).toBeInTheDocument();
  });
});

describe("DropdownMenu primitives", () => {
  it("opens on trigger click + renders items", async () => {
    render(
      <DropdownMenu>
        <DropdownMenuTrigger>menu</DropdownMenuTrigger>
        <DropdownMenuContent>
          <DropdownMenuGroup>
            <DropdownMenuLabel>actions</DropdownMenuLabel>
            <DropdownMenuItem onClick={vi.fn()}>
              rename
              <DropdownMenuShortcut>⌘R</DropdownMenuShortcut>
            </DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuItem>delete</DropdownMenuItem>
          </DropdownMenuGroup>
        </DropdownMenuContent>
      </DropdownMenu>,
    );
    await userEvent.click(screen.getByText("menu"));
    await waitFor(() =>
      expect(screen.getByText("rename")).toBeInTheDocument(),
    );
    expect(screen.getByText("actions")).toBeInTheDocument();
    expect(screen.getByText("⌘R")).toBeInTheDocument();
  });

  it("renders checkbox + radio items with portal + submenu", async () => {
    const onRadio = vi.fn();
    function Probe() {
      const [checked, setChecked] = React.useState(false);
      const [value, setValue] = React.useState("one");
      return (
        <DropdownMenu>
          <DropdownMenuTrigger>menu</DropdownMenuTrigger>
          <DropdownMenuPortal>
            <DropdownMenuContent>
              <DropdownMenuCheckboxItem
                checked={checked}
                onCheckedChange={setChecked}
              >
                toggle
              </DropdownMenuCheckboxItem>
              <DropdownMenuRadioGroup
                value={value}
                onValueChange={(v) => {
                  onRadio(v);
                  setValue(v);
                }}
              >
                <DropdownMenuRadioItem value="one">one</DropdownMenuRadioItem>
                <DropdownMenuRadioItem value="two">two</DropdownMenuRadioItem>
              </DropdownMenuRadioGroup>
              <DropdownMenuSub>
                <DropdownMenuSubTrigger>more</DropdownMenuSubTrigger>
                <DropdownMenuSubContent>
                  <DropdownMenuItem>nested</DropdownMenuItem>
                </DropdownMenuSubContent>
              </DropdownMenuSub>
            </DropdownMenuContent>
          </DropdownMenuPortal>
        </DropdownMenu>
      );
    }
    render(<Probe />);
    await userEvent.click(screen.getByText("menu"));
    await waitFor(() => expect(screen.getByText("toggle")).toBeInTheDocument());
    expect(screen.getByText("one")).toBeInTheDocument();
    expect(screen.getByText("two")).toBeInTheDocument();
    expect(screen.getByText("more")).toBeInTheDocument();
    // Click the second radio — selection propagates.
    await userEvent.click(screen.getByText("two"));
    expect(onRadio).toHaveBeenCalledWith("two");
  });
});

describe("Tooltip primitives", () => {
  it("mounts without throwing", () => {
    render(
      <TooltipProvider>
        <Tooltip>
          <TooltipTrigger>trigger</TooltipTrigger>
          <TooltipContent>hello</TooltipContent>
        </Tooltip>
      </TooltipProvider>,
    );
    expect(screen.getByText("trigger")).toBeInTheDocument();
  });
});

describe("Slider", () => {
  it("renders and forwards numeric onValueChange", async () => {
    const onChange = vi.fn();
    render(
      <Slider
        value={50}
        onValueChange={onChange}
        min={0}
        max={100}
        step={10}
        aria-label="test"
      />,
    );
    // Base-UI's slider renders a hidden <input type=range>. Changing it via
    // fireEvent is the only reliable way in happy-dom.
    const input = screen
      .getByLabelText("test")
      .querySelector('input[type="range"]') as HTMLInputElement;
    fireEvent.change(input, { target: { value: "70" } });
    await waitFor(() => expect(onChange).toHaveBeenCalled());
    expect(typeof onChange.mock.calls[0][0]).toBe("number");
  });

  it("accepts disabled prop", () => {
    render(
      <Slider
        value={0}
        onValueChange={() => {}}
        disabled
        aria-label="disabled"
      />,
    );
    expect(screen.getByLabelText("disabled")).toBeInTheDocument();
  });
});

describe("Switch", () => {
  it("toggles when clicked", async () => {
    const onCheckedChange = vi.fn();
    render(<Switch onCheckedChange={onCheckedChange} />);
    const sw = screen.getByRole("switch");
    await userEvent.click(sw);
    expect(onCheckedChange).toHaveBeenCalledWith(true, expect.anything());
  });

  it("accepts a size prop without throwing", () => {
    render(<Switch size="sm" />);
    expect(screen.getByRole("switch")).toBeInTheDocument();
  });
});
