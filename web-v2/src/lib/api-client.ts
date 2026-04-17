const TOKEN_KEY = "nexus_jwt";

export function getToken(): string | null {
  return localStorage.getItem(TOKEN_KEY);
}

export function setToken(token: string): void {
  localStorage.setItem(TOKEN_KEY, token);
}

export function clearToken(): void {
  localStorage.removeItem(TOKEN_KEY);
}

let unauthorizedHandler: (() => void) | null = null;

export function setUnauthorizedHandler(handler: () => void): void {
  unauthorizedHandler = handler;
}

interface APIResponse<T> {
  data?: T;
  error?: string;
}

export async function fetchAPI<T>(
  url: string,
  options: RequestInit = {},
): Promise<T> {
  const headers = new Headers(options.headers);
  const token = getToken();
  if (token) headers.set("Authorization", `Bearer ${token}`);
  const res = await fetch(url, { ...options, headers });
  // 401 means "session expired" — backend returns 400 for bad credentials.
  if (res.status === 401) {
    clearToken();
    unauthorizedHandler?.();
    throw new Error("Unauthorized");
  }
  const body: APIResponse<T> = await res.json();
  if (body.error) throw new Error(body.error);
  return body.data as T;
}
