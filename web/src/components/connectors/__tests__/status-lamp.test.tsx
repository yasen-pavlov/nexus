import { describe, expect, it } from "vitest";
import { screen } from "@testing-library/react";

import { StatusChip, StatusLamp } from "../status-lamp";
import { statusFromSync, type ConnectorStatus } from "../status";
import { render } from "@/test/test-utils";

describe("statusFromSync", () => {
  it("maps backend sync statuses onto UI ConnectorStatus", () => {
    expect(statusFromSync("running", "idle")).toBe("running");
    expect(statusFromSync("completed", "idle")).toBe("succeeded");
    expect(statusFromSync("failed", "idle")).toBe("failed");
    expect(statusFromSync("canceled", "idle")).toBe("canceled");
    expect(statusFromSync("interrupted", "idle")).toBe("interrupted");
  });

  it("returns the fallback when the sync status is undefined", () => {
    expect(statusFromSync(undefined, "disabled")).toBe("disabled");
    expect(statusFromSync(undefined, "idle")).toBe("idle");
  });
});

describe("StatusLamp", () => {
  it("renders a status role with the label as aria-label", () => {
    render(<StatusLamp status="running" />);
    const lamp = screen.getByRole("status");
    expect(lamp).toHaveAttribute("aria-label", "running");
  });

  it("applies the breathing animation class only for running", () => {
    const { rerender } = render(<StatusLamp status="idle" />);
    expect(screen.getByRole("status").className).not.toContain("lamp-breathe");
    rerender(<StatusLamp status="running" />);
    expect(screen.getByRole("status").className).toContain("lamp-breathe");
  });
});

describe("StatusChip", () => {
  it.each<[ConnectorStatus, string]>([
    ["idle", "Idle"],
    ["running", "Syncing"],
    ["succeeded", "Last sync OK"],
    ["failed", "Last sync failed"],
    ["canceled", "Canceled"],
    ["interrupted", "Interrupted"],
    ["disabled", "Disabled"],
  ])("shows the human label for %s", (status, label) => {
    render(<StatusChip status={status} />);
    expect(screen.getByText(label)).toBeInTheDocument();
  });

  it("renders the hint separator when a hint is supplied", () => {
    render(<StatusChip status="succeeded" hint="3h ago" />);
    expect(screen.getByText("· 3h ago")).toBeInTheDocument();
  });
});
