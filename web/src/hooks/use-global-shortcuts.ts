import { useEffect, useRef } from "react";

export type ChordKey = "s" | "c" | "a";

export interface GlobalShortcutHandlers {
  /** Cmd/Ctrl+K opens the command palette. */
  onPalette: () => void;
  /** `/` focuses search (caller decides nav + focus). */
  onSearchFocus: () => void;
  /** `?` opens the shortcuts cheat sheet. */
  onCheatSheet: () => void;
  /** vim chord: `g s` / `g c` / `g a`. */
  onChord: (key: ChordKey) => void;
  /** Set to false to silently disable while a higher-priority surface
   *  (login screen, modal etc.) wants the keyboard. */
  enabled?: boolean;
}

const CHORD_TIMEOUT_MS = 200;

function isEditable(el: EventTarget | null): boolean {
  if (!(el instanceof HTMLElement)) return false;
  const tag = el.tagName;
  if (tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT") return true;
  if (el.isContentEditable) return true;
  return false;
}

/**
 * One keydown listener for the whole app. Mount once near the top of the
 * tree (AppShell). Skips when focus is in an editable element so typing
 * "/" in a search box doesn't trigger search-focus, etc.
 *
 * Chord handling: when the user presses `g`, we open a 200 ms window for
 * a second key. The second key fires the chord; any other key (or timeout)
 * cancels.
 */
export function useGlobalShortcuts(handlers: GlobalShortcutHandlers) {
  // Stash handlers in a ref so the listener doesn't rebind every render.
  const handlersRef = useRef(handlers);
  handlersRef.current = handlers;

  useEffect(() => {
    let chordPending = false;
    let chordTimer: number | null = null;

    const cancelChord = () => {
      chordPending = false;
      if (chordTimer !== null) {
        window.clearTimeout(chordTimer);
        chordTimer = null;
      }
    };

    const onKeyDown = (e: KeyboardEvent) => {
      const h = handlersRef.current;
      if (h.enabled === false) return;

      // Don't steal keystrokes from editable surfaces — only Cmd/Ctrl+K
      // and Esc-style globals should fire mid-typing. We allow Cmd+K
      // so the palette opens even while the user is typing in the
      // search bar.
      const editable = isEditable(e.target);

      // Cmd/Ctrl + K → palette (always; works even in inputs).
      if ((e.metaKey || e.ctrlKey) && (e.key === "k" || e.key === "K")) {
        e.preventDefault();
        cancelChord();
        h.onPalette();
        return;
      }

      // Modifier-bearing keys other than the palette combo are not ours.
      if (e.metaKey || e.ctrlKey || e.altKey) return;

      if (editable) return;

      // `?` → cheat sheet. On US keyboards this is Shift+/ — some browsers
      // and Playwright report e.key === "?" while others report "/" with
      // shiftKey set. Accept either to be robust.
      if (e.key === "?" || (e.key === "/" && e.shiftKey)) {
        e.preventDefault();
        cancelChord();
        h.onCheatSheet();
        return;
      }

      // `/` → focus search.
      if (e.key === "/") {
        e.preventDefault();
        cancelChord();
        h.onSearchFocus();
        return;
      }

      // Chord state machine.
      if (chordPending) {
        const k = e.key.toLowerCase();
        if (k === "s" || k === "c" || k === "a") {
          e.preventDefault();
          cancelChord();
          h.onChord(k as ChordKey);
          return;
        }
        // Any other key — cancel the chord and let the key fall through.
        cancelChord();
        return;
      }

      if (e.key === "g" || e.key === "G") {
        e.preventDefault();
        chordPending = true;
        chordTimer = window.setTimeout(cancelChord, CHORD_TIMEOUT_MS);
        return;
      }
    };

    window.addEventListener("keydown", onKeyDown);
    return () => {
      cancelChord();
      window.removeEventListener("keydown", onKeyDown);
    };
  }, []);
}

/** Custom event the SearchBar listens for to focus its input. Dispatch
 *  via `dispatchFocusSearch()` so the contract lives in one place. */
export const FOCUS_SEARCH_EVENT = "nexus:focus-search";

export function dispatchFocusSearch() {
  window.dispatchEvent(new CustomEvent(FOCUS_SEARCH_EVENT));
}
