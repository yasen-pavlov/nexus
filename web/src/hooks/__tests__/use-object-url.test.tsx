import { afterEach, describe, expect, it, vi } from "vitest";
import { renderHook } from "@testing-library/react";

import { useObjectURL } from "../use-object-url";

afterEach(() => {
  vi.restoreAllMocks();
});

describe("useObjectURL", () => {
  it("mints a URL for a blob and revokes it on unmount", () => {
    const create = vi.spyOn(URL, "createObjectURL").mockReturnValue("blob:one");
    const revoke = vi.spyOn(URL, "revokeObjectURL").mockImplementation(() => {});
    const blob = new Blob(["a"]);

    const { result, unmount } = renderHook(() => useObjectURL(blob));

    expect(create).toHaveBeenCalledWith(blob);
    expect(result.current).toBe("blob:one");

    unmount();
    expect(revoke).toHaveBeenCalledWith("blob:one");
  });

  it("re-mints a fresh, un-revoked URL for a new consumer of the same blob", () => {
    // The core of the fix: because each consumer owns its object URL, a
    // remount (or a second component) mints a live URL instead of reusing a
    // revoked one — the cached Blob outlives any single consumer.
    const urls = ["blob:1", "blob:2"];
    let i = 0;
    vi.spyOn(URL, "createObjectURL").mockImplementation(() => urls[i++]);
    const revoke = vi.spyOn(URL, "revokeObjectURL").mockImplementation(() => {});
    const blob = new Blob(["a"]);

    const first = renderHook(() => useObjectURL(blob));
    expect(first.result.current).toBe("blob:1");
    first.unmount();
    expect(revoke).toHaveBeenCalledWith("blob:1");

    const second = renderHook(() => useObjectURL(blob));
    expect(second.result.current).toBe("blob:2");
    expect(revoke).not.toHaveBeenCalledWith("blob:2");
  });

  it("returns null when there is no blob", () => {
    const { result } = renderHook(() => useObjectURL(null));
    expect(result.current).toBeNull();
  });
});
