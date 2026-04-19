import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { waitFor } from "@testing-library/react";
import { render, screen, userEvent } from "@/test/test-utils";
import {
  InlineImage,
  InlineVideo,
  mimeIsImage,
  mimeIsVideo,
} from "../inline-media";

// --- IntersectionObserver mock shared by the visibility-gate tests.
// Exposes the last-created observer's trigger() so tests can simulate
// the element becoming visible without waiting on real layout.

interface FakeObserver {
  observe: (el: Element) => void;
  disconnect: () => void;
  trigger: (isIntersecting: boolean) => void;
}

function installIOMock() {
  const instances: FakeObserver[] = [];
  const Ctor = vi.fn(function (
    this: FakeObserver,
    cb: IntersectionObserverCallback,
  ) {
    let observed: Element | null = null;
    this.observe = (el: Element) => {
      observed = el;
    };
    this.disconnect = () => {
      observed = null;
    };
    this.trigger = (isIntersecting: boolean) => {
      if (!observed) return;
      cb(
        [
          {
            isIntersecting,
            target: observed,
          } as unknown as IntersectionObserverEntry,
        ],
        this as unknown as IntersectionObserver,
      );
    };
    instances.push(this);
  });
  (
    globalThis as unknown as { IntersectionObserver: typeof IntersectionObserver }
  ).IntersectionObserver = Ctor as unknown as typeof IntersectionObserver;
  return {
    latest: () => instances.at(-1)!,
    ctor: Ctor,
  };
}

describe("mime helpers", () => {
  it("recognises image/* and video/* prefixes", () => {
    expect(mimeIsImage("image/jpeg")).toBe(true);
    expect(mimeIsImage("image/webp")).toBe(true);
    expect(mimeIsImage("video/mp4")).toBe(false);
    expect(mimeIsImage(undefined)).toBe(false);
    expect(mimeIsVideo("video/mp4")).toBe(true);
    expect(mimeIsVideo("image/png")).toBe(false);
  });
});

describe("InlineImage", () => {
  it("renders an <img> with the fetched blob URL", async () => {
    render(<InlineImage id="d-img" filename="photo.jpg" />);
    const img = await screen.findByAltText("photo.jpg");
    expect(img.tagName).toBe("IMG");
    expect(img).toHaveAttribute("src", expect.stringMatching(/^blob:/));
  });

  it("opens a lightbox on click and closes on Escape", async () => {
    const user = userEvent.setup();
    render(<InlineImage id="d-img" filename="photo.jpg" />);

    // Wait for the fetched image to render.
    await screen.findByAltText("photo.jpg");

    // Click the thumbnail to open the lightbox.
    await user.click(screen.getAllByAltText("photo.jpg")[0]!);

    // Lightbox appears — role=dialog + the image rendered a second time at
    // the larger size.
    expect(await screen.findByRole("dialog")).toBeInTheDocument();
    expect(screen.getAllByAltText("photo.jpg")).toHaveLength(2);

    // Escape dismisses.
    await user.keyboard("{Escape}");
    await waitFor(() => {
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    });
  });

  it("closes the lightbox on backdrop click but not on inner click", async () => {
    const user = userEvent.setup();
    render(<InlineImage id="d-img" filename="photo.jpg" />);
    await screen.findByAltText("photo.jpg");
    await user.click(screen.getAllByAltText("photo.jpg")[0]!);

    const dialog = await screen.findByRole("dialog");
    // Clicking the lightbox image (inner) should NOT close.
    const bigImg = screen.getAllByAltText("photo.jpg")[1]!;
    await user.click(bigImg);
    expect(screen.queryByRole("dialog")).toBeInTheDocument();

    // Clicking the backdrop (dialog element itself) closes.
    await user.click(dialog);
    await waitFor(() => {
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    });
  });

  it("locks body scroll while the lightbox is open and restores it", async () => {
    const user = userEvent.setup();
    render(<InlineImage id="d-img" filename="photo.jpg" />);
    await screen.findByAltText("photo.jpg");

    expect(document.body.style.overflow).not.toBe("hidden");

    await user.click(screen.getAllByAltText("photo.jpg")[0]!);
    await screen.findByRole("dialog");
    expect(document.body.style.overflow).toBe("hidden");

    await user.keyboard("{Escape}");
    await waitFor(() => {
      expect(document.body.style.overflow).not.toBe("hidden");
    });
  });
});

describe("InlineVideo lazy loading", () => {
  let io: ReturnType<typeof installIOMock>;

  beforeEach(() => {
    io = installIOMock();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("does not render a <video> until the element intersects", async () => {
    render(<InlineVideo id="d-vid" filename="clip.mp4" />);

    // Before intersection: no <video> element, placeholder instead.
    expect(document.querySelector("video")).toBeNull();
    expect(screen.getByText(/clip\.mp4/)).toBeInTheDocument();

    // Fire the observer — simulate scrolling the element into view.
    io.latest().trigger(true);

    // After intersection, the <video> appears with the blob src.
    await waitFor(() => {
      expect(document.querySelector("video")).not.toBeNull();
    });
    const video = document.querySelector("video")!;
    expect(video.getAttribute("src")).toMatch(/^blob:/);
  });
});
