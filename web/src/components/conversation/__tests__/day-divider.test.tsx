import { describe, it, expect } from "vitest";
import { render, screen } from "@/test/test-utils";
import { DayDivider } from "../day-divider";

describe("DayDivider", () => {
  it("formats the label with weekday and date", () => {
    render(<DayDivider date={new Date("2026-04-10T12:00:00Z")} />);
    expect(screen.getByText(/Friday · Apr 10, 2026/)).toBeInTheDocument();
  });

  it("uses role=separator", () => {
    render(<DayDivider date={new Date("2026-04-10T12:00:00Z")} />);
    expect(screen.getByRole("separator")).toBeInTheDocument();
  });
});
