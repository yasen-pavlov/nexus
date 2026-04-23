import { useEffect, useState } from "react";

/**
 * Tween a numeric value toward a target using requestAnimationFrame.
 *
 * Motivating case: the sync progress SSE stream pushes ~400 progress
 * frames in under a second on a fast connector. React batches those
 * updates into a handful of renders, so a bare `{processed}` display
 * jumps in visible chunks (e.g. "5 → 26 → 47"). Feeding the target
 * value through this hook turns those chunks into a smooth tick.
 *
 * Semantics:
 * - The displayed value always approaches `target` at `speed` units
 *   per second, capped at the actual target — we never overshoot.
 * - A decrease in `target` (e.g. a failure rollback) snaps the
 *   displayed value down on the next rAF frame rather than tweening,
 *   because an animated countdown would misrepresent the state.
 * - If the hook is unmounted mid-tween, the pending rAF is
 *   cancelled.
 *
 * `speed` defaults high enough to keep up with bursty streams: 300
 * units/s is visibly smooth yet arrives at the target within ~1.5s
 * even after a 400-event backlog.
 */
export function useAnimatedNumber(target: number, speed = 300): number {
  const [displayed, setDisplayed] = useState(target);

  useEffect(() => {
    // All state reads/writes happen inside the rAF callback —
    // never synchronously in the effect body — so this plays
    // nicely with the react-hooks/set-state-in-effect lint.
    let raf = 0;
    let last: number | null = null;
    // Local mirror of the displayed value, captured at effect
    // start and advanced per frame. Lets us terminate the rAF
    // loop when we arrive at the target without probing React
    // state from inside a ref during render.
    let current = displayed;

    const tick = (ts: number) => {
      last ??= ts;
      const dt = (ts - last) / 1000;
      last = ts;

      // Regression — the connector rolled back a failed batch.
      // Snap the counter down rather than animating a
      // pretend-we-undid-the-work countdown.
      if (target < current) {
        current = target;
        setDisplayed(target);
        return;
      }
      if (target === current) {
        return;
      }

      current = Math.min(target, current + speed * dt);
      setDisplayed(current);
      if (current < target) {
        raf = requestAnimationFrame(tick);
      }
    };

    raf = requestAnimationFrame(tick);
    return () => {
      cancelAnimationFrame(raf);
    };
    // displayed is intentionally excluded — re-running the effect
    // on every animation frame would cancel/restart the rAF each
    // tick and produce the same behavior at higher cost. Target
    // changes (new SSE frame) and speed changes should restart;
    // routine displayed-value updates shouldn't.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [target, speed]);

  return Math.round(displayed);
}
