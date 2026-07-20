import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useState } from "react";

import { render, screen, userEvent, waitFor } from "@/test/test-utils";
import { ConversationView } from "../conversation-view";
import type { MessageRowModel } from "../message-row";

// Never auto-fires — these tests only need the sentinels observed, not
// intersection callbacks. observeCount lets a test wait for observers to
// activate, which happens on the same observersReady flip that seeds the
// compensation baseline — so it's a deterministic "baseline is seeded" signal.
let observeCount = 0;
class StubObserver {
  observe() {
    observeCount += 1;
  }
  unobserve() {}
  disconnect() {}
  takeRecords() {
    return [];
  }
}

// happy-dom does no layout, so drive scroll geometry manually via prototype
// getters/setters (the scroller is the only element these tests touch).
let stubHeight = 1000;
let stubTop = 0;
let origHeight: PropertyDescriptor | undefined;
let origTop: PropertyDescriptor | undefined;

beforeEach(() => {
  vi.stubGlobal("IntersectionObserver", StubObserver);
  observeCount = 0;
  stubHeight = 1000;
  stubTop = 0;
  origHeight = Object.getOwnPropertyDescriptor(HTMLElement.prototype, "scrollHeight");
  origTop = Object.getOwnPropertyDescriptor(HTMLElement.prototype, "scrollTop");
  Object.defineProperty(HTMLElement.prototype, "scrollHeight", {
    configurable: true,
    get: () => stubHeight,
  });
  Object.defineProperty(HTMLElement.prototype, "scrollTop", {
    configurable: true,
    get: () => stubTop,
    set: (v: number) => {
      stubTop = v;
    },
  });
});

afterEach(() => {
  vi.unstubAllGlobals();
  if (origHeight) Object.defineProperty(HTMLElement.prototype, "scrollHeight", origHeight);
  else delete (HTMLElement.prototype as { scrollHeight?: number }).scrollHeight;
  if (origTop) Object.defineProperty(HTMLElement.prototype, "scrollTop", origTop);
  else delete (HTMLElement.prototype as { scrollTop?: number }).scrollTop;
});

function row(sourceId: string): MessageRowModel {
  return {
    sourceId,
    senderId: null,
    senderName: "S",
    createdAt: "2026-06-03T00:00:00Z",
    body: sourceId,
    isSelf: false,
    isAnchor: false,
    position: "solo",
  };
}

const baseProps = {
  sourceType: "telegram",
  isLoadingInitial: false,
  isFetchingOlder: false,
  isFetchingNewer: false,
  hasOlder: true,
  hasNewer: false,
  onOlderIntersect: () => {},
  onNewerIntersect: () => {},
};

// Stateful harness so the ConversationView instance stays mounted across the
// rows change (a plain rerender remounts, which resets the refs and only
// exercises the positioning effect, not the compensation).
function Harness({
  initialRows,
  nextRows,
}: Readonly<{ initialRows: MessageRowModel[]; nextRows: MessageRowModel[] }>) {
  const [rows, setRows] = useState(initialRows);
  return (
    <>
      <button type="button" onClick={() => setRows(nextRows)}>
        load
      </button>
      <ConversationView {...baseProps} rows={rows} />
    </>
  );
}

describe("ConversationView older-prepend scroll compensation", () => {
  it("advances scrollTop by the height delta when older rows are prepended", async () => {
    render(
      <Harness
        initialRows={[row("m3"), row("m4")]}
        nextRows={[row("m1"), row("m2"), row("m3"), row("m4")]}
      />,
    );

    // Positioning scrolls to the bottom; observers activating means the
    // observersReady flip fired and the compensation baseline is seeded.
    await waitFor(() => expect(stubTop).toBe(1000));
    await waitFor(() => expect(observeCount).toBeGreaterThan(0));

    // Prepend an older page and grow the scroll height by 600px.
    stubHeight = 1600;
    await userEvent.click(screen.getByRole("button", { name: "load" }));

    // Viewport preserved: scrollTop += (newHeight - oldHeight) = 1000 + 600.
    await waitFor(() => expect(stubTop).toBe(1600));
  });

  it("auto-fetches the older page when the window filters down to zero rows", async () => {
    // An all-stickers/polls Telegram page filters to no visible rows. The
    // observers never arm (positioning bails on rows.length === 0), so the
    // view must pull the adjacent page itself to avoid dead-ending.
    const onOlder = vi.fn();
    render(
      <ConversationView
        {...baseProps}
        rows={[]}
        hasOlder
        hasNewer={false}
        onOlderIntersect={onOlder}
      />,
    );
    await waitFor(() => expect(onOlder).toHaveBeenCalled());
  });

  it("auto-fetches the newer page when zero rows and only newer remains", async () => {
    const onNewer = vi.fn();
    render(
      <ConversationView
        {...baseProps}
        rows={[]}
        hasOlder={false}
        hasNewer
        onNewerIntersect={onNewer}
      />,
    );
    await waitFor(() => expect(onNewer).toHaveBeenCalled());
  });

  it("does NOT auto-fetch while a page is already in flight", () => {
    const onOlder = vi.fn();
    render(
      <ConversationView
        {...baseProps}
        rows={[]}
        hasOlder
        hasNewer={false}
        isFetchingOlder
        onOlderIntersect={onOlder}
      />,
    );
    expect(onOlder).not.toHaveBeenCalled();
  });

  it("does NOT move scrollTop on a newer append (rows[0] unchanged)", async () => {
    render(
      <Harness
        initialRows={[row("m1"), row("m2")]}
        nextRows={[row("m1"), row("m2"), row("m3")]}
      />,
    );
    await waitFor(() => expect(stubTop).toBe(1000));
    await waitFor(() => expect(observeCount).toBeGreaterThan(0));

    // Append at the tail — rows[0] is still m1, so no compensation.
    stubHeight = 1600;
    await userEvent.click(screen.getByRole("button", { name: "load" }));

    await new Promise((r) => setTimeout(r, 20));
    expect(stubTop).toBe(1000);
  });
});
