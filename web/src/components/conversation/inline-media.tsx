import { useState, useRef, useEffect, useCallback } from "react";
import { createPortal } from "react-dom";
import { FileWarning, Loader2, X } from "lucide-react";
import { cn } from "@/lib/utils";
import { useDocumentBlob } from "@/hooks/use-document-blob";

interface InlineImageProps {
  id: string;
  filename: string;
}

export function InlineImage({ id, filename }: InlineImageProps) {
  const { data, isLoading, isError } = useDocumentBlob(id);
  const [open, setOpen] = useState(false);

  if (isLoading) return <MediaPlaceholder label="Loading…" />;
  if (isError || !data) return <MediaError label={filename} />;

  return (
    <>
      <button
        type="button"
        onClick={() => setOpen(true)}
        className={cn(
          // w-fit makes the button shrink to the image's natural width
          // instead of spanning the bubble — otherwise portrait/square
          // photos leave an awkward empty band on the right.
          "block w-fit cursor-zoom-in overflow-hidden rounded-md transition-[filter]",
          "hover:brightness-95 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary/40",
        )}
      >
        <img
          src={data}
          alt={filename}
          loading="lazy"
          className="block max-h-[320px] max-w-full object-contain"
        />
      </button>
      {open && (
        <Lightbox onClose={() => setOpen(false)} filename={filename}>
          <img
            src={data}
            alt={filename}
            className="max-h-[90vh] max-w-[90vw] rounded-md object-contain shadow-2xl"
          />
        </Lightbox>
      )}
    </>
  );
}

interface InlineVideoProps {
  id: string;
  filename: string;
}

// InlineVideo renders a playable video. We lazy-load the blob only
// when the component actually becomes visible — videos can be tens of
// megabytes and the window can hold dozens of messages, so eager
// fetching every video would blow through bandwidth and memory.
export function InlineVideo({ id, filename }: InlineVideoProps) {
  const wrapperRef = useRef<HTMLDivElement>(null);
  const [visible, setVisible] = useState(false);
  const [open, setOpen] = useState(false);

  useEffect(() => {
    const el = wrapperRef.current;
    if (!el || visible) return;
    const obs = new IntersectionObserver(
      (entries) => {
        if (entries.some((e) => e.isIntersecting)) {
          setVisible(true);
          obs.disconnect();
        }
      },
      { rootMargin: "200px 0px" },
    );
    obs.observe(el);
    return () => obs.disconnect();
  }, [visible]);

  const { data, isLoading, isError } = useDocumentBlob(id, visible);

  return (
    <div
      ref={wrapperRef}
      className="relative w-fit max-w-full overflow-hidden rounded-md"
    >
      {!visible || isLoading ? (
        <MediaPlaceholder label={filename} />
      ) : isError || !data ? (
        <MediaError label={filename} />
      ) : (
        <>
          <video
            src={data}
            controls
            preload="metadata"
            className="block max-h-[360px] max-w-full"
          >
            <track kind="captions" />
          </video>
          <button
            type="button"
            onClick={() => setOpen(true)}
            aria-label="Expand video"
            className={cn(
              "absolute right-2 top-2 inline-flex items-center gap-1 rounded-md bg-background/80 px-2 py-1 text-[11px] font-medium text-foreground backdrop-blur-sm",
              "hover:bg-background focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary/40",
            )}
          >
            Expand
          </button>
        </>
      )}
      {open && data && (
        <Lightbox onClose={() => setOpen(false)} filename={filename}>
          <video
            src={data}
            controls
            autoPlay
            className="max-h-[90vh] max-w-[90vw] rounded-md shadow-2xl"
          >
            <track kind="captions" />
          </video>
        </Lightbox>
      )}
    </div>
  );
}

// Lightbox is a fixed-position overlay that dims the page and centers
// its content. Dismisses on background click, Escape key, or the
// close button. Portals to document.body so it can escape any
// ancestor with overflow:hidden (the AppShell scroller, the
// conversation scroller, the message bubble).
interface LightboxProps {
  filename: string;
  onClose: () => void;
  children: React.ReactNode;
}

function Lightbox({ filename, onClose, children }: LightboxProps) {
  // Memoize onClose so the keydown effect doesn't re-bind per render.
  const handleClose = useCallback(() => onClose(), [onClose]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") handleClose();
    };
    window.addEventListener("keydown", onKey);
    // Prevent body scroll while the lightbox is open.
    const prev = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    return () => {
      window.removeEventListener("keydown", onKey);
      document.body.style.overflow = prev;
    };
  }, [handleClose]);

  return createPortal(
    <div
      role="dialog"
      aria-modal="true"
      aria-label={filename}
      onClick={handleClose}
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/85 p-6 backdrop-blur-sm"
    >
      <button
        type="button"
        onClick={handleClose}
        aria-label="Close"
        className="absolute right-4 top-4 inline-flex size-9 items-center justify-center rounded-full bg-background/80 text-foreground backdrop-blur-sm transition-colors hover:bg-background focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary/40"
      >
        <X className="size-4" aria-hidden />
      </button>
      <div onClick={(e) => e.stopPropagation()}>{children}</div>
    </div>,
    document.body,
  );
}

function MediaPlaceholder({ label }: { label: string }) {
  return (
    <div className="flex h-40 w-72 items-center justify-center gap-2 text-[12px] text-muted-foreground">
      <Loader2 className="size-3.5 animate-spin" aria-hidden />
      <span className="truncate">{label}</span>
    </div>
  );
}

function MediaError({ label }: { label: string }) {
  return (
    <div className="flex h-32 w-64 items-center justify-center gap-2 text-[12px] italic text-muted-foreground/80">
      <FileWarning className="size-3.5" aria-hidden />
      <span className="truncate">Failed to load · {label}</span>
    </div>
  );
}
