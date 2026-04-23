import { useEffect, useRef, useState } from "react";

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
 * - The displayed value always approaches `target` monotonically at
 *   `speed` units per second, capped by the actual value — we never
 *   overshoot or visit a negative delta.
 * - A decrease in `target` (e.g. a failure rollback) snaps the
 *   displayed value down immediately rather than tweening, because
 *   an animated countdown would misrepresent the state.
 * - If the hook is unmounted mid-tween, the RAF is cancelled.
 *
 * `speed` defaults high enough to keep up with bursty streams: 300
 * units/s is visibly smooth yet arrives at the target within ~1.5s
 * even after a 400-event backlog.
 */
export function useAnimatedNumber(target: number, speed = 300): number {
  const [displayed, setDisplayed] = useState(target);
  const rafRef = useRef<number | null>(null);
  const lastTsRef = useRef<number | null>(null);
  const currentRef = useRef(target);

  // Keep a ref synced with the rendered value so the RAF callback
  // reads the latest without retriggering the effect.
  currentRef.current = displayed;

  useEffect(() => {
    // Immediate snap for regressions (rollback on failed batch) —
    // tweening down would pretend work undid itself over time,
    // which is misleading.
    if (target < displayed) {
      setDisplayed(target);
      currentRef.current = target;
      if (rafRef.current !== null) {
        cancelAnimationFrame(rafRef.current);
        rafRef.current = null;
        lastTsRef.current = null;
      }
      return;
    }
    if (target === displayed) {
      return;
    }

    const tick = (ts: number) => {
      if (lastTsRef.current === null) {
        lastTsRef.current = ts;
      }
      const dt = (ts - lastTsRef.current) / 1000;
      lastTsRef.current = ts;
      const next = Math.min(target, currentRef.current + speed * dt);
      setDisplayed(next);
      currentRef.current = next;
      if (next < target) {
        rafRef.current = requestAnimationFrame(tick);
      } else {
        rafRef.current = null;
        lastTsRef.current = null;
      }
    };
    rafRef.current = requestAnimationFrame(tick);
    return () => {
      if (rafRef.current !== null) {
        cancelAnimationFrame(rafRef.current);
        rafRef.current = null;
        lastTsRef.current = null;
      }
    };
  }, [target, displayed, speed]);

  return Math.round(displayed);
}
