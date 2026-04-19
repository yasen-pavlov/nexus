import { createFileRoute, redirect } from "@tanstack/react-router";
import { getToken } from "@/lib/api-client";
import { LoginForm } from "@/components/login-form";

export const Route = createFileRoute("/login")({
  beforeLoad: () => {
    if (getToken()) throw redirect({ to: "/" });
  },
  component: LoginPage,
});

function LoginPage() {
  return (
    <div className="flex min-h-screen items-center justify-center bg-background">
      <LoginForm />
    </div>
  );
}
