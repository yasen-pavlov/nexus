import { http, HttpResponse } from "msw";
import type { User, AuthResponse, HealthResponse } from "@/lib/api-types";

const fakeAdmin: User = { id: "u1", username: "admin", role: "admin" };
const fakeUser: User = { id: "u2", username: "viewer", role: "user" };
const fakeToken = "fake-jwt-token";
const fakeUserToken = "fake-user-token";

function wrapData<T>(data: T) {
  return { data };
}

export const handlers = [
  http.get("*/api/health", () =>
    HttpResponse.json(wrapData<HealthResponse>({ status: "ok" })),
  ),

  http.post("*/api/auth/login", async ({ request }) => {
    const body = (await request.json()) as {
      username: string;
      password: string;
    };
    if (body.username === "admin" && body.password === "password123") {
      return HttpResponse.json(
        wrapData<AuthResponse>({ token: fakeToken, user: fakeAdmin }),
      );
    }
    if (body.username === "viewer" && body.password === "password123") {
      return HttpResponse.json(
        wrapData<AuthResponse>({ token: fakeUserToken, user: fakeUser }),
      );
    }
    return HttpResponse.json({ error: "invalid credentials" }, { status: 400 });
  }),

  http.post("*/api/auth/register", async ({ request }) => {
    const body = (await request.json()) as {
      username: string;
      password: string;
    };
    return HttpResponse.json(
      wrapData<AuthResponse>({
        token: fakeToken,
        user: { ...fakeAdmin, username: body.username },
      }),
    );
  }),

  http.get("*/api/auth/me", ({ request }) => {
    const auth = request.headers.get("Authorization");
    if (auth === `Bearer ${fakeToken}`) {
      return HttpResponse.json(wrapData(fakeAdmin));
    }
    if (auth === `Bearer ${fakeUserToken}`) {
      return HttpResponse.json(wrapData(fakeUser));
    }
    return HttpResponse.json({ error: "Unauthorized" }, { status: 401 });
  }),
];

export { fakeAdmin, fakeUser, fakeToken, fakeUserToken };
