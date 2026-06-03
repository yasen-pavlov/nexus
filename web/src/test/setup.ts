import "@testing-library/jest-dom/vitest";
import { cleanup } from "@testing-library/react";
import { afterEach, beforeAll, afterAll, beforeEach } from "vitest";
import { server } from "./mocks/server";

// Node 25 ships its own built-in localStorage, but it's only usable when
// the process was started with `--localstorage-file=<path>`. Without
// that flag, the global `localStorage` object exists but its methods
// (clear/setItem/etc.) are not callable — and Node's stub shadows
// happy-dom's perfectly good Storage implementation. Install a tiny
// in-memory polyfill that survives both environments. Has to run
// before any test imports a hook that touches localStorage.
function installInMemoryStorage(target: "localStorage" | "sessionStorage") {
  const store = new Map<string, string>();
  const polyfill = {
    get length() {
      return store.size;
    },
    clear() {
      store.clear();
    },
    getItem(key: string) {
      return store.get(key) ?? null;
    },
    setItem(key: string, value: string) {
      store.set(key, String(value));
    },
    removeItem(key: string) {
      store.delete(key);
    },
    key(i: number) {
      return Array.from(store.keys())[i] ?? null;
    },
  };
  Object.defineProperty(globalThis, target, {
    configurable: true,
    writable: true,
    value: polyfill,
  });
}
installInMemoryStorage("localStorage");
installInMemoryStorage("sessionStorage");

beforeAll(() => server.listen({ onUnhandledRequest: "error" }));
beforeEach(() => {
  localStorage.clear();
  sessionStorage.clear();
});
afterEach(() => {
  server.resetHandlers();
  cleanup();
});
afterAll(() => server.close());

// happy-dom doesn't stub URL.createObjectURL / revokeObjectURL.
// Components that render authed blob URLs need these to be callable
// during rendering; without the stub, <img src={URL.createObjectURL(..)}>
// throws TypeError and the test never gets past first render.
if (typeof URL.createObjectURL !== "function") {
  (URL as unknown as { createObjectURL: (b: Blob) => string }).createObjectURL =
    () => "blob:mock";
}
if (typeof URL.revokeObjectURL !== "function") {
  (URL as unknown as { revokeObjectURL: (u: string) => void }).revokeObjectURL =
    () => undefined;
}
