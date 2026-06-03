const TOKEN_KEY = "nexus_jwt";

export function getToken(): string | null {
  return localStorage.getItem(TOKEN_KEY);
}

export function setToken(token: string): void {
  localStorage.setItem(TOKEN_KEY, token);
}

export function clearToken(): void {
  localStorage.removeItem(TOKEN_KEY);
}

let unauthorizedHandler: (() => void) | null = null;

export function setUnauthorizedHandler(handler: () => void): void {
  unauthorizedHandler = handler;
}

interface APIResponse<T> {
  data?: T;
  error?: string;
}

export async function fetchAPI<T>(
  url: string,
  options: RequestInit = {},
): Promise<T> {
  const headers = new Headers(options.headers);
  const token = getToken();
  if (token) headers.set("Authorization", `Bearer ${token}`);
  const res = await fetch(url, { ...options, headers });
  // 401 means "session expired" — backend returns 400 for bad credentials.
  if (res.status === 401) {
    clearToken();
    unauthorizedHandler?.();
    throw new Error("Unauthorized");
  }
  // 204 No Content is a real success path (DELETE flows). Any caller
  // that types T as void doesn't expect a body anyway.
  if (res.status === 204) return undefined as T;
  const body: APIResponse<T> = await res.json();
  if (body.error) throw new Error(body.error);
  return body.data as T;
}

// openSyncProgressSSE opens the multiplexed sync-progress EventSource and
// wires handlers for JSON-parsed frames + errors. EventSource cannot set
// custom headers, so we piggyback the JWT as a query param (`?token=...`) —
// the backend's auth middleware accepts this as a fallback for exactly
// this use case. Returns the EventSource so the caller can close() it on
// unmount.
export function openSyncProgressSSE<T = unknown>(
  onMessage: (frame: T) => void,
  onError?: (e: Event) => void,
): EventSource | null {
  const token = getToken();
  if (!token) return null;
  // happy-dom (used in Vitest) doesn't ship an EventSource implementation;
  // guard so hooks that mount in tests don't blow up. The production
  // browser bundle always has one.
  if (typeof EventSource === "undefined") return null;
  const url = `/api/sync/progress?token=${encodeURIComponent(token)}`;
  const es = new EventSource(url);
  es.onmessage = (e) => {
    if (!e.data) return;
    try {
      const parsed = JSON.parse(e.data) as T;
      onMessage(parsed);
    } catch {
      // Ignore unparseable frames (e.g. future non-JSON comments).
    }
  };
  if (onError) es.onerror = onError;
  return es;
}

/**
 * One parsed SSE frame. `event` is the named event (or the empty string
 * for unnamed frames); `data` is the post-`data:`-line payload, joined
 * across multiple `data:` lines per the spec. Comments and `id:` /
 * `retry:` lines are dropped by the parser.
 */
export interface SSEFrame {
  event: string;
  data: string;
}

/**
 * Open a streaming POST request that emits SSE frames. Used by the chat
 * messages endpoint, which returns `text/event-stream` over POST.
 *
 * EventSource is GET-only and can't set an Authorization header, so we
 * manage the stream ourselves with fetch + ReadableStream and a small
 * line-buffered SSE parser. Tolerates frames split across chunks,
 * multi-line `data:` (joined with `\n` per spec), `:` comment lines
 * (proxy keep-alives), and `\r\n` line endings.
 *
 * Cancel via the AbortController passed in. The hook layer swallows the
 * resulting AbortError on user-initiated cancel; the BE persists a
 * partial assistant message marked cancelled.
 */
export async function* openChatMessageStream(
  chatID: string,
  body: { content: string; model?: string },
  signal: AbortSignal,
): AsyncGenerator<SSEFrame, void, void> {
  const headers = new Headers({ "Content-Type": "application/json" });
  const token = getToken();
  if (token) headers.set("Authorization", `Bearer ${token}`);

  const res = await fetch(`/api/chats/${chatID}/messages`, {
    method: "POST",
    headers,
    body: JSON.stringify(body),
    signal,
  });

  if (res.status === 401) {
    clearToken();
    unauthorizedHandler?.();
    throw new Error("Unauthorized");
  }
  if (!res.ok) {
    // Try to surface the backend's `{error}` envelope on a non-2xx
    // pre-stream failure; fall back to a generic HTTP message.
    let msg = `HTTP ${res.status}`;
    try {
      const env = (await res.json()) as { error?: string };
      if (env.error) msg = env.error;
    } catch {
      // body wasn't JSON — leave msg as the HTTP code
    }
    throw new Error(msg);
  }
  if (!res.body) {
    throw new Error("response body missing");
  }

  const reader = res.body.pipeThrough(new TextDecoderStream()).getReader();
  let buf = "";
  // Accumulators for the in-progress frame. SSE spec says a frame ends
  // when we see a blank line; we keep accumulating event/data lines
  // until then.
  let event = "";
  const dataLines: string[] = [];

  const dispatch = (): SSEFrame | null => {
    if (event === "" && dataLines.length === 0) return null;
    const frame: SSEFrame = { event, data: dataLines.join("\n") };
    event = "";
    dataLines.length = 0;
    return frame;
  };

  try {
    while (true) {
      const { value, done } = await reader.read();
      if (done) break;
      buf += value;

      // Split on \n; keep the trailing partial line (if any) in `buf`.
      let nl = buf.indexOf("\n");
      while (nl !== -1) {
        let line = buf.slice(0, nl);
        buf = buf.slice(nl + 1);
        // Strip the spec's optional CR before LF.
        if (line.endsWith("\r")) line = line.slice(0, -1);
        if (line === "") {
          const f = dispatch();
          if (f) yield f;
        } else if (line.startsWith(":")) {
          // SSE comment / keep-alive — ignore
        } else if (line.startsWith("event:")) {
          event = line.slice(6).trimStart();
        } else if (line.startsWith("data:")) {
          dataLines.push(line.slice(5).trimStart());
        }
        // id:/retry: lines fall through silently — we don't need them
        nl = buf.indexOf("\n");
      }
    }
    // Flush any final frame that didn't end with a blank line.
    const f = dispatch();
    if (f) yield f;
  } finally {
    reader.releaseLock();
  }
}

// fetchAuthedBlob fetches an authenticated binary resource (e.g. a
// cached avatar) and returns an object URL the caller can assign to an
// <img src>. Caller is responsible for revoking via URL.revokeObjectURL
// when the image unmounts. Returns null when the resource doesn't
// exist — callers render a fallback.
export async function fetchAuthedBlob(url: string): Promise<string | null> {
  const headers = new Headers();
  const token = getToken();
  if (token) headers.set("Authorization", `Bearer ${token}`);
  const res = await fetch(url, { headers });
  if (res.status === 401) {
    clearToken();
    unauthorizedHandler?.();
    throw new Error("Unauthorized");
  }
  if (res.status === 404) return null;
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  const blob = await res.blob();
  return URL.createObjectURL(blob);
}
