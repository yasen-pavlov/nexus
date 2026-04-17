export interface User {
  id: string;
  username: string;
  role: "admin" | "user";
}

export interface AuthResponse {
  token: string;
  user: User;
}

export interface HealthResponse {
  status: string;
  setup_required?: boolean;
}
