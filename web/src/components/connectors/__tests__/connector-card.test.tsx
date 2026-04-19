import { describe, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

import { ConnectorCard, type ConnectorRow } from "../connector-card";
import { renderWithRouter } from "@/test/test-utils";

function row(overrides: Partial<ConnectorRow> = {}): ConnectorRow {
  return {
    id: "c-1",
    type: "filesystem",
    name: "notes",
    status: "idle",
    enabled: true,
    shared: false,
    schedule: "",
    ...overrides,
  };
}

function renderCard(
  r: ConnectorRow,
  handlers: Partial<Parameters<typeof ConnectorCard>[0]> = {},
) {
  const base = {
    onSync: vi.fn(),
    onCancel: vi.fn(),
    onResetCursor: vi.fn(),
    onDelete: vi.fn(),
    canManage: true,
  };
  const props = { row: r, ...base, ...handlers };
  renderWithRouter(<ConnectorCard {...props} />, {
    extraRoutes: ["/connectors/$id"],
  });
  return props;
}

describe("ConnectorCard", () => {
  it("renders name, type, and default schedule label when no cron is set", async () => {
    renderCard(row());
    await waitFor(() => {
      expect(screen.getByText("notes")).toBeInTheDocument();
    });
    expect(screen.getByText("Filesystem")).toBeInTheDocument();
    // schedule="" + enabled=true → "Manual trigger"
    expect(screen.getByText(/manual trigger/i)).toBeInTheDocument();
  });

  it("shows 'Disabled' badge + schedule label when enabled is false", async () => {
    renderCard(row({ enabled: false }));
    await waitFor(() => {
      // There's a literal "Disabled" badge + the schedule text "Disabled".
      expect(screen.getAllByText(/disabled/i).length).toBeGreaterThan(0);
    });
  });

  it("shows the Shared badge when shared is true", async () => {
    renderCard(row({ shared: true }));
    await waitFor(() => {
      expect(screen.getByText(/shared/i)).toBeInTheDocument();
    });
  });

  it("renders the running progress bar and Cancel button when a sync is live", async () => {
    renderCard(
      row({
        status: "running",
        sync: { jobId: "j-1", processed: 45, total: 100, errors: 0 },
      }),
    );
    await waitFor(() => {
      expect(screen.getByRole("button", { name: /cancel/i })).toBeInTheDocument();
    });
    // progressbar role from sync-progress-bar
    const bar = screen.getByRole("progressbar");
    expect(bar).toHaveAttribute("aria-valuenow", "45");
  });

  it("fires onSync with the connector id when Sync is clicked", async () => {
    const user = userEvent.setup();
    const { onSync } = renderCard(row({ id: "c-42" }));
    await waitFor(() => {
      expect(screen.getByRole("button", { name: /^sync$/i })).toBeInTheDocument();
    });
    await user.click(screen.getByRole("button", { name: /^sync$/i }));
    expect(onSync).toHaveBeenCalledWith("c-42");
  });

  it("fires onCancel with the job id when Cancel is clicked", async () => {
    const user = userEvent.setup();
    const { onCancel } = renderCard(
      row({
        status: "running",
        sync: { jobId: "job-42", processed: 10, total: 100, errors: 0 },
      }),
    );
    await waitFor(() => {
      expect(screen.getByRole("button", { name: /cancel/i })).toBeInTheDocument();
    });
    await user.click(screen.getByRole("button", { name: /cancel/i }));
    expect(onCancel).toHaveBeenCalledWith("job-42");
  });

  it("disables Sync when enabled is false", async () => {
    renderCard(row({ enabled: false }));
    await waitFor(() => {
      expect(screen.getByRole("button", { name: /^sync$/i })).toBeDisabled();
    });
  });

  it("disables Sync when canManage is false (shared + non-admin)", async () => {
    renderCard(row({ shared: true }), { canManage: false });
    await waitFor(() => {
      expect(screen.getByRole("button", { name: /^sync$/i })).toBeDisabled();
    });
  });

  it("surfaces the failure hint when status=failed", async () => {
    renderCard(row({ status: "failed" }));
    await waitFor(() => {
      expect(screen.getByText(/last sync failed/i)).toBeInTheDocument();
    });
  });

  it("renders identity inline when a telegram identity is present", async () => {
    renderCard(
      row({
        type: "telegram",
        identity: { name: "Yasen P.", hasAvatar: false },
      }),
    );
    await waitFor(() => {
      expect(screen.getByText("Yasen P.")).toBeInTheDocument();
    });
  });
});
