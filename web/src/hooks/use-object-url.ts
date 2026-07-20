import { useEffect, useState } from "react";

// useObjectURL turns a Blob into an object URL whose lifetime is tied to the
// calling component: it mints a fresh URL on mount (and whenever the blob
// identity changes) and revokes it on unmount. Because each consumer owns its
// own URL, one component unmounting can't invalidate a URL another is still
// using, and a cached Blob can be re-minted into a live URL on every remount.
//
// Returns null while there is no blob or before the URL is minted for the
// current blob. Callers that need to distinguish "still preparing" from "no
// image" should gate on their query's loading state as well.
export function useObjectURL(blob: Blob | null | undefined): string | null {
  const [url, setUrl] = useState<string | null>(null);

  useEffect(() => {
    const objectURL = blob ? URL.createObjectURL(blob) : null;
    // An object URL is a managed, side-effectful resource, not derived state:
    // createObjectURL must run inside the effect so the cleanup below revokes
    // exactly the committed URL (correct under StrictMode's mount/remount,
    // where useMemo would double-create and leak), and its handle is surfaced
    // via state. This is the rule's sanctioned "connect to an external
    // system" case, so the synchronous setState is intentional.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setUrl(objectURL);
    return () => {
      if (objectURL) URL.revokeObjectURL(objectURL);
    };
  }, [blob]);

  return url;
}
