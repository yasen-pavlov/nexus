import { createFileRoute } from "@tanstack/react-router";

import { AccountPage } from "@/components/account/account-page";

export const Route = createFileRoute("/_authenticated/account")({
  component: AccountRoute,
});

function AccountRoute() {
  const { user } = Route.useRouteContext();
  return <AccountPage user={user} />;
}
