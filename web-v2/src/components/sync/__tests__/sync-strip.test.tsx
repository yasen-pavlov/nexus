import { describe, expect, it } from "vitest";
import { screen, waitFor } from "@testing-library/react";

import { SyncStrip } from "../sync-strip";
import { renderWithRouter } from "@/test/test-utils";

function renderStrip(props: React.ComponentProps<typeof SyncStrip>) {
  // The strip is a <Link to="/connectors">. Register /connectors so the
  // click target has an href (and asserts below don't hit silent nav fails).
  return renderWithRouter(<SyncStrip {...props} />, {
    extraRoutes: ["/connectors"],
  });
}

// renderWithRouter boots the TanStack router asynchronously — the first
// commit is empty. Wrap each query in waitFor.
const findStripLink = (pattern: RegExp) =>
  waitFor(() => screen.getByRole("link", { name: pattern }));

describe("SyncStrip", () => {
  it("renders the idle trust strip with source count + last-sync hint", async () => {
    renderStrip({
      stats: { sourceCount: 3, lastSyncAt: new Date().toISOString() },
      running: [],
      totalActive: 0,
    });
    const link = await findStripLink(/view connectors/i);
    expect(link).toHaveAttribute("href", "/connectors");
    expect(link.textContent).toContain("3");
    expect(link.textContent).toContain("sources");
    expect(link.textContent?.toLowerCase()).toContain("synced");
  });

  it("uses the singular 'source' when sourceCount is 1", async () => {
    renderStrip({
      stats: { sourceCount: 1 },
      running: [],
      totalActive: 0,
    });
    const link = await findStripLink(/view connectors/i);
    expect(link.textContent ?? "").toMatch(/1\s+source(?!s)/);
  });

  it("flips to the running aggregate when a sync is active", async () => {
    renderStrip({
      stats: { sourceCount: 3 },
      running: [
        { connectorName: "my-imap", processed: 45, total: 100 },
        { connectorName: "my-telegram", processed: 10, total: 20 },
      ],
      totalActive: 2,
    });
    const link = await findStripLink(/syncing 2 of 2/i);
    expect(link.textContent).toContain("my-imap");
    expect(link.textContent).toContain("Syncing 2/2");
  });

  it("shows the leader's own percentage when its total is known", async () => {
    renderStrip({
      stats: { sourceCount: 1 },
      running: [{ connectorName: "big-mail", processed: 250, total: 1000 }],
      totalActive: 1,
    });
    const link = await findStripLink(/syncing 1 of 1/i);
    expect(link.textContent).toContain("big-mail");
    expect(link.textContent).toContain("25%");
  });

  it("hides the leader percentage when total is 0 (discovering phase)", async () => {
    renderStrip({
      stats: { sourceCount: 1 },
      running: [{ connectorName: "new-mailbox", processed: 0, total: 0 }],
      totalActive: 1,
    });
    const link = await findStripLink(/syncing 1 of 1/i);
    expect(link.textContent).toContain("new-mailbox");
    expect(link.textContent).not.toContain("%");
  });
});
