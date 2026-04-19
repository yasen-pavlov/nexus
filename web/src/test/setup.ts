import "@testing-library/jest-dom/vitest";
import { cleanup } from "@testing-library/react";
import { afterEach, beforeAll, afterAll, beforeEach } from "vitest";
import { server } from "./mocks/server";

beforeAll(() => server.listen({ onUnhandledRequest: "error" }));
beforeEach(() => {
  localStorage.clear();
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
