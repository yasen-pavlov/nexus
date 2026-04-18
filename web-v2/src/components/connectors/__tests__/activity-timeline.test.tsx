import { describe, expect, it } from "vitest";
import { screen } from "@testing-library/react";

import { ActivityTimeline } from "../activity-timeline";
import type { SyncRun } from "@/lib/api-types";
import { render } from "@/test/test-utils";

function run(overrides: Partial<SyncRun> = {}): SyncRun {
  return {
    id: "r-1",
    connector_id: "c-1",
    status: "completed",
    docs_total: 10,
    docs_processed: 10,
    docs_deleted: 0,
    errors: 0,
    error_message: "",
    started_at: "2026-04-10T12:00:00Z",
    completed_at: "2026-04-10T12:05:00Z",
    ...overrides,
  };
}

describe("ActivityTimeline", () => {
  it("renders the empty state when runs is empty", () => {
    render(<ActivityTimeline runs={[]} />);
    expect(
      screen.getByText(/no sync runs yet/i),
    ).toBeInTheDocument();
  });

  it("renders a row per run with processed count", () => {
    render(
      <ActivityTimeline
        runs={[
          run({ id: "r-1", docs_processed: 123 }),
          run({ id: "r-2", started_at: "2026-04-09T08:00:00Z", docs_processed: 7 }),
        ]}
      />,
    );
    expect(screen.getByText(/123 indexed/)).toBeInTheDocument();
    expect(screen.getByText(/7 indexed/)).toBeInTheDocument();
  });

  it("surfaces docs_deleted only when non-zero", () => {
    render(
      <ActivityTimeline
        runs={[run({ id: "r-clean", docs_deleted: 0 })]}
      />,
    );
    expect(screen.queryByText(/removed/i)).not.toBeInTheDocument();

    render(
      <ActivityTimeline
        runs={[run({ id: "r-dirty", docs_deleted: 4 })]}
      />,
    );
    expect(screen.getByText(/4 removed/)).toBeInTheDocument();
  });

  it("renders the failure block with the error message when status=failed", () => {
    render(
      <ActivityTimeline
        runs={[run({ status: "failed", errors: 1, error_message: "imap: connection refused" })]}
      />,
    );
    expect(screen.getByText(/1 error/i)).toBeInTheDocument();
    expect(screen.getByText(/imap: connection refused/)).toBeInTheDocument();
  });

  it("renders 'running' duration label when completed_at is absent", () => {
    render(
      <ActivityTimeline
        runs={[run({ status: "running", completed_at: undefined })]}
      />,
    );
    expect(screen.getByText("running")).toBeInTheDocument();
  });

  it("accepts the interrupted status without crashing", () => {
    // Pre-regression check for the new status added in Phase 3.
    render(
      <ActivityTimeline
        runs={[run({ status: "interrupted", error_message: "Nexus restarted" })]}
      />,
    );
    expect(screen.getByText(/Nexus restarted/)).toBeInTheDocument();
  });
});
