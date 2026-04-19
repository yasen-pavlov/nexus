import { describe, it, expect, vi, beforeEach, afterEach, type Mock } from "vitest";
import { render } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

import { useGlobalShortcuts, type ChordKey } from "../use-global-shortcuts";

interface Spies {
  onPalette: Mock<() => void>;
  onSearchFocus: Mock<() => void>;
  onCheatSheet: Mock<() => void>;
  onChord: Mock<(key: ChordKey) => void>;
}

function Harness(spies: Spies & { enabled?: boolean }) {
  useGlobalShortcuts({
    onPalette: spies.onPalette,
    onSearchFocus: spies.onSearchFocus,
    onCheatSheet: spies.onCheatSheet,
    onChord: spies.onChord,
    enabled: spies.enabled,
  });
  return (
    <div>
      <input data-testid="some-input" placeholder="type here" />
    </div>
  );
}

function makeSpies(): Spies {
  return {
    onPalette: vi.fn<() => void>(),
    onSearchFocus: vi.fn<() => void>(),
    onCheatSheet: vi.fn<() => void>(),
    onChord: vi.fn<(key: ChordKey) => void>(),
  };
}

describe("useGlobalShortcuts", () => {
  beforeEach(() => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it("Cmd+K and Ctrl+K both fire the palette callback", async () => {
    const spies = makeSpies();
    render(<Harness {...spies} />);
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });

    await user.keyboard("{Meta>}k{/Meta}");
    expect(spies.onPalette).toHaveBeenCalledTimes(1);

    await user.keyboard("{Control>}k{/Control}");
    expect(spies.onPalette).toHaveBeenCalledTimes(2);
  });

  it("/ focuses search and ? opens cheat sheet outside editable elements", async () => {
    const spies = makeSpies();
    render(<Harness {...spies} />);
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });

    await user.keyboard("/");
    expect(spies.onSearchFocus).toHaveBeenCalledTimes(1);

    await user.keyboard("?");
    expect(spies.onCheatSheet).toHaveBeenCalledTimes(1);
  });

  it("does not fire / or ? when typing inside an input", async () => {
    const spies = makeSpies();
    const { getByTestId } = render(<Harness {...spies} />);
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });

    const input = getByTestId("some-input") as HTMLInputElement;
    input.focus();

    await user.keyboard("/");
    await user.keyboard("?");
    expect(spies.onSearchFocus).not.toHaveBeenCalled();
    expect(spies.onCheatSheet).not.toHaveBeenCalled();
  });

  it("Cmd+K still fires from inside an input (so the palette is reachable while typing)", async () => {
    const spies = makeSpies();
    const { getByTestId } = render(<Harness {...spies} />);
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });

    (getByTestId("some-input") as HTMLInputElement).focus();
    await user.keyboard("{Meta>}k{/Meta}");
    expect(spies.onPalette).toHaveBeenCalledTimes(1);
  });

  it("g + s within 200 ms fires the 's' chord", async () => {
    const spies = makeSpies();
    render(<Harness {...spies} />);
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });

    await user.keyboard("g");
    await user.keyboard("s");
    expect(spies.onChord).toHaveBeenCalledWith("s");
  });

  it("g + c fires the 'c' chord", async () => {
    const spies = makeSpies();
    render(<Harness {...spies} />);
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });

    await user.keyboard("g");
    await user.keyboard("c");
    expect(spies.onChord).toHaveBeenCalledWith("c");
  });

  it("g + a fires the 'a' chord", async () => {
    const spies = makeSpies();
    render(<Harness {...spies} />);
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });

    await user.keyboard("g");
    await user.keyboard("a");
    expect(spies.onChord).toHaveBeenCalledWith("a");
  });

  it("g followed by an unrelated key cancels the chord", async () => {
    const spies = makeSpies();
    render(<Harness {...spies} />);
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });

    await user.keyboard("g");
    await user.keyboard("x");
    expect(spies.onChord).not.toHaveBeenCalled();
  });

  it("g times out after 200 ms — second key after timeout does not chord", () => {
    const spies = makeSpies();
    render(<Harness {...spies} />);

    // dispatch a raw keydown for 'g'
    window.dispatchEvent(new KeyboardEvent("keydown", { key: "g" }));
    vi.advanceTimersByTime(250);
    window.dispatchEvent(new KeyboardEvent("keydown", { key: "s" }));
    expect(spies.onChord).not.toHaveBeenCalled();
  });

  it("does not fire any handler when enabled=false", async () => {
    const spies = makeSpies();
    render(<Harness {...spies} enabled={false} />);
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });

    await user.keyboard("{Meta>}k{/Meta}");
    await user.keyboard("/");
    await user.keyboard("?");
    await user.keyboard("g");
    await user.keyboard("s");

    expect(spies.onPalette).not.toHaveBeenCalled();
    expect(spies.onSearchFocus).not.toHaveBeenCalled();
    expect(spies.onCheatSheet).not.toHaveBeenCalled();
    expect(spies.onChord).not.toHaveBeenCalled();
  });
});
