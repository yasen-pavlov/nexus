// sanitizeHighlight makes a server-provided search-highlight fragment safe to
// inject via dangerouslySetInnerHTML. The server requests OpenSearch
// encoder=html, so fragment text is already HTML-escaped; this is defense in
// depth against an unescaped or legacy index.
//
// The approach is provably safe: escape every angle bracket so no tag can
// form, then re-introduce literal brackets ONLY for the exact highlight tags
// Nexus emits (<mark>/<em>, no attributes). Anything else — other tags, any
// attribute, event handlers — stays escaped and renders as inert text. Note we
// intentionally do NOT touch `&`, so content the server already escaped
// (e.g. `&lt;img&gt;`) keeps displaying correctly rather than double-escaping.
const ALLOWED_TAG = /&lt;(\/?)(mark|em)&gt;/g;

export function sanitizeHighlight(html: string): string {
  const escaped = html.replaceAll("<", "&lt;").replaceAll(">", "&gt;");
  return escaped.replace(ALLOWED_TAG, "<$1$2>");
}
