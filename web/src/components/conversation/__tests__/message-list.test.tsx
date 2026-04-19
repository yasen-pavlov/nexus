import { describe, it, expect } from "vitest";
import { buildTimeline } from "../timeline";
import type { MessageRowModel } from "../message-row";

function row(
  sourceId: string,
  senderId: string,
  createdAt: string,
  overrides: Partial<MessageRowModel> = {},
): MessageRowModel {
  return {
    sourceId,
    senderId,
    senderName: senderId,
    createdAt,
    body: sourceId,
    isSelf: false,
    isAnchor: false,
    position: "solo",
    ...overrides,
  };
}

describe("buildTimeline", () => {
  it("inserts a day divider before each new calendar day", () => {
    const rows = [
      row("a1", "alice", "2026-04-10T18:00:00Z"),
      row("a2", "alice", "2026-04-11T09:00:00Z"),
    ];
    const items = buildTimeline(rows);
    expect(items.map((i) => i.kind)).toEqual(["day", "message", "day", "message"]);
  });

  it("assigns group positions from same-sender bursts within 5 minutes", () => {
    const rows = [
      row("a1", "alice", "2026-04-10T18:00:00Z"),
      row("a2", "alice", "2026-04-10T18:01:00Z"),
      row("a3", "alice", "2026-04-10T18:02:00Z"),
      row("b1", "bob", "2026-04-10T18:03:00Z"),
    ];
    const items = buildTimeline(rows);
    const messages = items.filter((i) => i.kind === "message") as Array<{
      kind: "message";
      model: MessageRowModel;
    }>;
    expect(messages[0]!.model.position).toBe("first");
    expect(messages[1]!.model.position).toBe("mid");
    expect(messages[2]!.model.position).toBe("last");
    expect(messages[3]!.model.position).toBe("solo");
  });

  it("breaks a burst when the gap exceeds the threshold", () => {
    const rows = [
      row("a1", "alice", "2026-04-10T18:00:00Z"),
      row("a2", "alice", "2026-04-10T18:30:00Z"),
    ];
    const items = buildTimeline(rows);
    const messages = items.filter((i) => i.kind === "message") as Array<{
      kind: "message";
      model: MessageRowModel;
    }>;
    expect(messages[0]!.model.position).toBe("solo");
    expect(messages[1]!.model.position).toBe("solo");
  });

  it("never groups messages with null sender ids", () => {
    const rows = [
      row("a1", "alice", "2026-04-10T18:00:00Z", { senderId: null }),
      row("a2", "alice", "2026-04-10T18:01:00Z", { senderId: null }),
    ];
    const items = buildTimeline(rows);
    const messages = items.filter((i) => i.kind === "message") as Array<{
      kind: "message";
      model: MessageRowModel;
    }>;
    expect(messages[0]!.model.position).toBe("solo");
    expect(messages[1]!.model.position).toBe("solo");
  });
});
