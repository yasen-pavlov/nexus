/**
 * `cardOwnsSnippet` reports whether a source's per-card body renders the
 * matched snippet itself (inside its own tinted layout), so the surrounding
 * chassis should NOT also render the headline — otherwise the same text shows
 * twice. Shared by the search ResultCard and the Ask evidence card.
 */
export function cardOwnsSnippet(sourceType: string): boolean {
  return (
    sourceType === "imap" ||
    sourceType === "paperless" ||
    sourceType === "filesystem" ||
    sourceType === "ical"
  );
}
