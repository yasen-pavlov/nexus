// Curated view of the auto-generated schema. The generated types mark
// everything optional (swaggo/swag v1 doesn't emit `required`) and don't
// know about the `{data, error}` response wrapper, so we re-export key
// schemas with tighter types here. Regenerate via `npm run gen:types`
// whenever the Go backend changes its swagger annotations.
import type { components } from "./api-schema";

type Schemas = components["schemas"];

// Unwrap + require: strip the generated `?` that swagger 2.0 forces on every
// field. Apply to response shapes the backend always fills in.
type Req<T> = { [K in keyof T]-?: NonNullable<T[K]> };

export type User = Omit<Req<Schemas["internal_api.userResponse"]>, "role"> & {
  role: "admin" | "user";
};

export type AuthResponse = Omit<Req<Schemas["internal_api.authResponse"]>, "user"> & {
  user: User;
};

export interface HealthResponse {
  status: string;
  setup_required?: boolean;
}
