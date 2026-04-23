import { describe, expect, it } from "vitest";
import { renderHook, waitFor } from "@testing-library/react";

import { useAnimatedNumber } from "../use-animated-number";

describe("useAnimatedNumber", () => {
  it("returns the initial target immediately on first render", () => {
    const { result } = renderHook(() => useAnimatedNumber(50));
    expect(result.current).toBe(50);
  });

  it("tweens up toward a higher target over time", async () => {
    const { result, rerender } = renderHook(
      ({ target }) => useAnimatedNumber(target, 200),
      { initialProps: { target: 0 } },
    );
    rerender({ target: 30 });
    await waitFor(() => {
      // Eventually reaches the target; the hook advances at 200
      // units/s, so even on slow CI we arrive within a fraction of
      // a second.
      expect(result.current).toBe(30);
    });
  });

  it("snaps down immediately when the target regresses", () => {
    // Regressions correspond to a pipeline-flush failure rolling
    // back the optimistic processed bumps — animating a countdown
    // would pretend work undid itself over time.
    const { result, rerender } = renderHook(
      ({ target }) => useAnimatedNumber(target, 200),
      { initialProps: { target: 100 } },
    );
    rerender({ target: 80 });
    expect(result.current).toBe(80);
  });

  it("stays put when the target doesn't change", async () => {
    const { result, rerender } = renderHook(
      ({ target }) => useAnimatedNumber(target),
      { initialProps: { target: 42 } },
    );
    rerender({ target: 42 });
    // A short wait without target movement should leave the value
    // untouched — proves the effect short-circuits when next ===
    // displayed.
    await new Promise((resolve) => setTimeout(resolve, 50));
    expect(result.current).toBe(42);
  });
});
