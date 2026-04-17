import { createFileRoute } from "@tanstack/react-router";

export const Route = createFileRoute("/_authenticated/admin/users")({
  component: UsersPage,
});

function UsersPage() {
  return (
    <div className="flex flex-1 flex-col gap-4 p-4">
      <h1 className="text-2xl font-semibold">Users</h1>
      <p className="text-muted-foreground">
        User management. Coming in Phase 4.
      </p>
    </div>
  );
}
