export type ConnectorStatus =
  | "idle"
  | "running"
  | "succeeded"
  | "failed"
  | "canceled"
  | "interrupted"
  | "disabled";

/**
 * Map a backend SyncStatus ("running" | "completed" | "failed" | "canceled")
 * plus the connector-level "is this enabled?" hint into the wider UI
 * ConnectorStatus enum used by the card / detail page. "completed" becomes
 * "succeeded" so the lamp color keys to sage rather than plain marmalade.
 */
export function statusFromSync(
  sync:
    | "running"
    | "completed"
    | "failed"
    | "canceled"
    | "interrupted"
    | undefined,
  fallback: ConnectorStatus,
): ConnectorStatus {
  switch (sync) {
    case "running":
      return "running";
    case "completed":
      return "succeeded";
    case "failed":
      return "failed";
    case "canceled":
      return "canceled";
    case "interrupted":
      return "interrupted";
    default:
      return fallback;
  }
}
