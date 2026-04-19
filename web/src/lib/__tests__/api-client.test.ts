import {
  afterEach,
  beforeEach,
  describe,
  expect,
  it,
  vi,
} from "vitest";
import { http, HttpResponse } from "msw";

import { server } from "@/test/mocks/server";
import {
  fetchAPI,
  fetchAuthedBlob,
  getToken,
  setToken,
  clearToken,
  setUnauthorizedHandler,
} from "../api-client";

afterEach(() => {
  server.resetHandlers();
  setUnauthorizedHandler(() => {});
});

describe("token storage", () => {
  it("set/get/clear round-trip", () => {
    expect(getToken()).toBeNull();
    setToken("abc");
    expect(getToken()).toBe("abc");
    clearToken();
    expect(getToken()).toBeNull();
  });
});

describe("fetchAPI", () => {
  it("attaches Authorization when a token is present", async () => {
    setToken("tok");
    let observed = "";
    server.use(
      http.get("*/api/auth/me", ({ request }) => {
        observed = request.headers.get("Authorization") ?? "";
        return HttpResponse.json({ data: { id: "u1" } });
      }),
    );
    const data = await fetchAPI<{ id: string }>("/api/auth/me");
    expect(observed).toBe("Bearer tok");
    expect(data.id).toBe("u1");
  });

  it("skips Authorization when no token is set", async () => {
    let observed: string | null = "present";
    server.use(
      http.get("*/api/health", ({ request }) => {
        observed = request.headers.get("Authorization");
        return HttpResponse.json({ data: { status: "ok" } });
      }),
    );
    await fetchAPI("/api/health");
    expect(observed).toBeNull();
  });

  it("clears token and fires handler on 401", async () => {
    setToken("tok");
    const onUnauth = vi.fn();
    setUnauthorizedHandler(onUnauth);
    server.use(
      http.get("*/api/auth/me", () =>
        new HttpResponse(null, { status: 401 }),
      ),
    );
    await expect(fetchAPI("/api/auth/me")).rejects.toThrow(/unauthorized/i);
    expect(getToken()).toBeNull();
    expect(onUnauth).toHaveBeenCalled();
  });

  it("throws the error string from a JSON error envelope", async () => {
    server.use(
      http.get("*/api/nope", () =>
        HttpResponse.json({ error: "bad input" }, { status: 400 }),
      ),
    );
    await expect(fetchAPI("/api/nope")).rejects.toThrow("bad input");
  });
});

describe("fetchAuthedBlob", () => {
  beforeEach(() => {
    setToken("tok");
  });

  it("returns an object URL for a 200 response", async () => {
    server.use(
      http.get("*/api/blob", () =>
        new HttpResponse(new Blob(["x"]), { status: 200 }),
      ),
    );
    const url = await fetchAuthedBlob("/api/blob");
    expect(url).toMatch(/^blob:/);
  });

  it("returns null on 404", async () => {
    server.use(
      http.get("*/api/missing", () =>
        new HttpResponse(null, { status: 404 }),
      ),
    );
    expect(await fetchAuthedBlob("/api/missing")).toBeNull();
  });

  it("clears token on 401", async () => {
    server.use(
      http.get("*/api/blob", () =>
        new HttpResponse(null, { status: 401 }),
      ),
    );
    await expect(fetchAuthedBlob("/api/blob")).rejects.toThrow(/unauthorized/i);
    expect(getToken()).toBeNull();
  });

  it("throws HTTP N on other non-ok", async () => {
    server.use(
      http.get("*/api/blob", () =>
        new HttpResponse(null, { status: 500 }),
      ),
    );
    await expect(fetchAuthedBlob("/api/blob")).rejects.toThrow(/HTTP 500/);
  });
});
