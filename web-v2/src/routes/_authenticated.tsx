import { createFileRoute, Outlet, redirect } from "@tanstack/react-router";
import { getToken, fetchAPI } from "@/lib/api-client";
import type { User } from "@/lib/api-types";
import { authKeys } from "@/lib/query-keys";
import { AppShell } from "@/components/app-shell";

export const Route = createFileRoute("/_authenticated")({
  beforeLoad: async ({ context }) => {
    const token = getToken();
    if (!token) throw redirect({ to: "/login" });

    let user = context.queryClient.getQueryData<User>(authKeys.me());
    if (!user) {
      try {
        user = await context.queryClient.fetchQuery({
          queryKey: authKeys.me(),
          queryFn: () => fetchAPI<User>("/api/auth/me"),
        });
      } catch {
        throw redirect({ to: "/login" });
      }
    }
    return { user: user! };
  },
  component: AuthenticatedLayout,
});

function AuthenticatedLayout() {
  const { user } = Route.useRouteContext();
  return (
    <AppShell user={user}>
      <Outlet />
    </AppShell>
  );
}
