import { createFileRoute } from "@tanstack/react-router";
import { useMemo, useState } from "react";
import { useForm, useWatch, type Control } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import {
  AtSign,
  Eye,
  EyeOff,
  KeyRound,
  MoreHorizontal,
  ShieldCheck,
  Trash2,
  UserPlus,
  UserRound,
  Users as UsersIcon,
} from "lucide-react";
import {
  createColumnHelper,
  flexRender,
  getCoreRowModel,
  useReactTable,
} from "@tanstack/react-table";

import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Sheet,
  SheetContent,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet";
import { Skeleton } from "@/components/ui/skeleton";

import { ChangePasswordSheet } from "@/components/admin/change-password-sheet";
import { SettingsSection } from "@/components/admin/settings-section";
import { TypedConfirmDialog } from "@/components/admin/typed-confirm-dialog";
import { UsersMobileList } from "@/components/admin/users-mobile-list";

import { useIsMobile } from "@/hooks/use-mobile";
import { useUsers, type AdminUserRow } from "@/hooks/use-users";
import { formatAbsolute, formatRelative } from "@/lib/format";
import { cn } from "@/lib/utils";

export const Route = createFileRoute("/_authenticated/admin/users")({
  component: UsersPage,
});

function UsersPage() {
  const { user: currentUser } = Route.useRouteContext();
  const { data, isPending, create, remove } = useUsers();
  const isMobile = useIsMobile();

  const [newOpen, setNewOpen] = useState(false);
  const [passwordTarget, setPasswordTarget] = useState<
    { userId: string; label: string } | null
  >(null);
  const [deleteTarget, setDeleteTarget] = useState<AdminUserRow | null>(null);

  const rows = data ?? [];
  const self = rows.find((u) => u.id === currentUser.id);

  return (
    <div className="mx-auto w-full max-w-4xl flex-1 px-6 py-8">
      <header className="mb-8">
        <h1 className="text-[20px] font-medium tracking-[-0.005em] text-foreground">
          Users
        </h1>
        <p className="mt-1 text-[13.5px] leading-[1.55] text-muted-foreground">
          The humans Nexus indexes for. Roles gate admin-only routes; search
          scoping is automatic — each user only sees their own and shared
          content.
        </p>
      </header>

      <div className="flex flex-col gap-10">
        <SettingsSection
          id="roster"
          label="Roster · 01"
          title="Accounts"
          icon={UserRound}
          description="Who can sign in."
          actions={
            <Button
              type="button"
              size="sm"
              className="gap-1.5"
              onClick={() => setNewOpen(true)}
            >
              <UserPlus className="h-3.5 w-3.5" />
              New user
            </Button>
          }
        >
          {isPending ? (
            <div className="flex flex-col gap-2">
              <Skeleton className="h-12 w-full" />
              <Skeleton className="h-12 w-full" />
            </div>
          ) : rows.length === 0 ? (
            <EmptyRoster onNew={() => setNewOpen(true)} />
          ) : isMobile ? (
            <UsersMobileList
              rows={rows}
              currentUserId={currentUser.id}
              onChangePassword={(u) =>
                setPasswordTarget({
                  userId: u.id,
                  label:
                    u.id === currentUser.id
                      ? "your password"
                      : `${u.username}'s password`,
                })
              }
              onDelete={(u) => setDeleteTarget(u)}
            />
          ) : (
            <UsersTable
              rows={rows}
              currentUserId={currentUser.id}
              onChangePassword={(u) =>
                setPasswordTarget({
                  userId: u.id,
                  label:
                    u.id === currentUser.id
                      ? "your password"
                      : `${u.username}'s password`,
                })
              }
              onDelete={(u) => setDeleteTarget(u)}
            />
          )}
        </SettingsSection>

        {self && (
          <SettingsSection
            id="self"
            label="You · 02"
            title="Your account"
            icon={ShieldCheck}
            description="Rotate your own password."
          >
            <div className="flex items-center gap-4">
              <InitialsTile username={self.username} />
              <div className="min-w-0 flex-1 leading-tight">
                <div className="flex items-center gap-2">
                  <span className="text-[14px] font-medium text-foreground">
                    {self.username}
                  </span>
                  <RoleBadge role={self.role} />
                </div>
                <div className="mt-0.5 text-[12px] text-muted-foreground">
                  Joined{" "}
                  <span
                    title={formatAbsolute(self.created_at)}
                    className="text-foreground/80"
                  >
                    {formatRelative(self.created_at)}
                  </span>
                </div>
              </div>
              <Button
                type="button"
                variant="outline"
                size="sm"
                className="gap-1.5"
                onClick={() =>
                  setPasswordTarget({
                    userId: self.id,
                    label: "your password",
                  })
                }
              >
                <KeyRound className="h-3.5 w-3.5" />
                Change password
              </Button>
            </div>
          </SettingsSection>
        )}
      </div>

      <NewUserSheet
        open={newOpen}
        onOpenChange={setNewOpen}
        onCreate={async (values) => {
          await create.mutateAsync(values);
          setNewOpen(false);
        }}
        isPending={create.isPending}
      />

      {passwordTarget && (
        <ChangePasswordSheet
          open
          onOpenChange={(v) => {
            if (!v) setPasswordTarget(null);
          }}
          userId={passwordTarget.userId}
          label={passwordTarget.label}
        />
      )}

      {deleteTarget && (
        <TypedConfirmDialog
          open
          onOpenChange={(v) => {
            if (!v) setDeleteTarget(null);
          }}
          eyebrow="Remove account"
          title="Remove account?"
          icon={Trash2}
          body={
            <span>
              Removes{" "}
              <span className="font-medium text-foreground">
                {deleteTarget.username}
              </span>{" "}
              and detaches every connector they own. Content they marked
              shared stays searchable; private content drops out on the next
              sync.
            </span>
          }
          confirmPhrase={deleteTarget.username}
          confirmLabel={`Remove ${deleteTarget.username}`}
          variant="destructive"
          onConfirm={async () => {
            await remove.mutateAsync(deleteTarget.id);
            setDeleteTarget(null);
          }}
        />
      )}
    </div>
  );
}

// --- Table ------------------------------------------------------------------

const columnHelper = createColumnHelper<AdminUserRow>();

interface UsersTableProps {
  rows: AdminUserRow[];
  currentUserId: string;
  onChangePassword: (u: AdminUserRow) => void;
  onDelete: (u: AdminUserRow) => void;
}

function UsersTable({
  rows,
  currentUserId,
  onChangePassword,
  onDelete,
}: UsersTableProps) {
  const columns = useMemo(
    () => [
      columnHelper.accessor("username", {
        header: () => <span>User</span>,
        cell: (info) => {
          const u = info.row.original;
          const isSelf = u.id === currentUserId;
          return (
            <div className="flex min-w-0 items-center gap-3">
              <InitialsTile username={u.username} />
              <span className="min-w-0 truncate text-[13.5px] font-medium text-foreground">
                {u.username}
              </span>
              {isSelf && <YouBadge />}
            </div>
          );
        },
      }),
      columnHelper.accessor("role", {
        header: () => <span>Role</span>,
        cell: (info) => <RoleBadge role={info.getValue()} />,
      }),
      columnHelper.accessor((row) => row.created_at ?? "", {
        id: "created_at",
        header: () => <span className="block text-right">Joined</span>,
        cell: (info) => {
          const iso = info.getValue() as string | undefined;
          return (
            <div
              className="text-right text-[12.5px] tabular-nums text-muted-foreground"
              title={formatAbsolute(iso)}
            >
              {formatRelative(iso)}
            </div>
          );
        },
      }),
      columnHelper.display({
        id: "actions",
        header: () => <span className="sr-only">Actions</span>,
        cell: (info) => {
          const u = info.row.original;
          const isSelf = u.id === currentUserId;
          return (
            <div className="text-right">
              <DropdownMenu>
                <DropdownMenuTrigger
                  aria-label={`Actions for ${u.username}`}
                  className="inline-flex size-8 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
                >
                  <MoreHorizontal className="size-4" aria-hidden />
                </DropdownMenuTrigger>
                <DropdownMenuContent align="end" className="w-48">
                  {/* onClick (not onSelect) — base-ui Menu.Item's onSelect
                      swallows/defers the click in a way that leaves the
                      follow-up Sheet/Dialog never mounting. Matches the
                      connector-card pattern that already works. */}
                  <DropdownMenuItem
                    onClick={() => onChangePassword(u)}
                    className="gap-2"
                  >
                    <KeyRound className="size-3.5" aria-hidden />
                    Change password
                  </DropdownMenuItem>
                  <DropdownMenuSeparator />
                  <DropdownMenuItem
                    disabled={isSelf}
                    onClick={() => {
                      if (isSelf) return;
                      onDelete(u);
                    }}
                    className={cn(
                      "gap-2 text-destructive focus:text-destructive",
                      isSelf && "cursor-not-allowed opacity-50",
                    )}
                  >
                    <Trash2 className="size-3.5" aria-hidden />
                    {isSelf ? "Can't delete yourself" : "Delete account"}
                  </DropdownMenuItem>
                </DropdownMenuContent>
              </DropdownMenu>
            </div>
          );
        },
      }),
    ],
    [currentUserId, onChangePassword, onDelete],
  );

  // TanStack Table's useReactTable returns methods that intentionally
  // aren't memoized — React Compiler can't auto-memoize components that
  // consume it. That's a library-level constraint, not a local smell, so
  // we explicitly opt out of the warning here.
  // eslint-disable-next-line react-hooks/incompatible-library
  const table = useReactTable({
    data: rows,
    columns,
    getCoreRowModel: getCoreRowModel(),
  });

  return (
    <div className="flex flex-col">
      <div className="grid grid-cols-[minmax(0,2fr)_minmax(0,1fr)_minmax(0,1fr)_auto] items-center gap-4 border-b border-border/60 px-3 pb-2 text-[10px] font-semibold uppercase tracking-[0.08em] text-muted-foreground/80">
        {table.getHeaderGroups()[0].headers.map((header) => (
          <div key={header.id}>
            {flexRender(header.column.columnDef.header, header.getContext())}
          </div>
        ))}
      </div>
      <div className="divide-y divide-border/60">
        {table.getRowModel().rows.map((row) => (
          <div
            key={row.id}
            className="grid grid-cols-[minmax(0,2fr)_minmax(0,1fr)_minmax(0,1fr)_auto] items-center gap-4 rounded-md px-3 py-3 transition-colors hover:bg-card-hover"
          >
            {row.getVisibleCells().map((cell) => (
              <div key={cell.id} className="min-w-0">
                {flexRender(cell.column.columnDef.cell, cell.getContext())}
              </div>
            ))}
          </div>
        ))}
      </div>
    </div>
  );
}

// --- Primitives -------------------------------------------------------------

function InitialsTile({ username }: { username: string }) {
  const initials = username.slice(0, 2).toUpperCase();
  return (
    <span
      aria-hidden
      className="flex size-7 shrink-0 items-center justify-center rounded-md bg-primary/15 text-[11px] font-semibold text-primary"
    >
      {initials}
    </span>
  );
}

function YouBadge() {
  return (
    <span className="rounded-full bg-primary/15 px-1.5 py-0.5 text-[10px] font-semibold uppercase tracking-[0.08em] text-primary">
      you
    </span>
  );
}

function RoleBadge({ role }: { role: "admin" | "user" }) {
  if (role === "admin") {
    return (
      <span
        className="inline-flex items-center gap-1 rounded-md px-1.5 py-0.5 text-[11px] font-semibold uppercase tracking-[0.08em]"
        style={{
          backgroundColor:
            "color-mix(in oklch, var(--primary) 14%, transparent)",
          color: "var(--primary)",
        }}
      >
        <ShieldCheck className="size-3" aria-hidden />
        admin
      </span>
    );
  }
  return (
    <span className="inline-flex items-center gap-1.5 rounded-md bg-muted px-1.5 py-0.5 text-[11px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">
      <span
        aria-hidden
        className="size-1.5 rounded-full bg-muted-foreground/50"
      />
      user
    </span>
  );
}

function EmptyRoster({ onNew }: { onNew: () => void }) {
  return (
    <div className="flex flex-col items-center gap-3 py-10 text-center">
      <div className="flex size-11 items-center justify-center rounded-xl bg-primary/15 text-primary">
        <UsersIcon className="size-5" aria-hidden />
      </div>
      <div>
        <div className="text-[14px] font-medium text-foreground">
          No users yet
        </div>
        <div className="text-[12.5px] text-muted-foreground">
          Create the first account to get started.
        </div>
      </div>
      <Button type="button" size="sm" className="gap-1.5" onClick={onNew}>
        <UserPlus className="h-3.5 w-3.5" />
        New user
      </Button>
    </div>
  );
}

// --- New user sheet ---------------------------------------------------------

const newUserSchema = z.object({
  username: z
    .string()
    .min(1, "Username required")
    .max(64, "Max 64 characters")
    .regex(/^[a-zA-Z0-9_.-]+$/, "Letters, digits, . _ - only"),
  password: z.string().min(8, "At least 8 characters"),
  role: z.enum(["user", "admin"]),
});
type NewUserValues = z.infer<typeof newUserSchema>;

function NewUserSheet({
  open,
  onOpenChange,
  onCreate,
  isPending,
}: {
  open: boolean;
  onOpenChange: (v: boolean) => void;
  onCreate: (values: NewUserValues) => Promise<void>;
  isPending: boolean;
}) {
  const [showPassword, setShowPassword] = useState(false);
  const form = useForm<NewUserValues>({
    resolver: zodResolver(newUserSchema),
    defaultValues: { username: "", password: "", role: "user" },
  });

  const submit = form.handleSubmit(async (values) => {
    await onCreate(values);
    form.reset({ username: "", password: "", role: "user" });
    setShowPassword(false);
  });

  return (
    <Sheet
      open={open}
      onOpenChange={(v) => {
        onOpenChange(v);
        if (!v) {
          form.reset({ username: "", password: "", role: "user" });
          setShowPassword(false);
        }
      }}
    >
      <SheetContent
        side="right"
        className="flex w-full flex-col p-0 sm:max-w-md"
      >
        <SheetHeader className="border-b border-border px-6 py-4">
          <SheetTitle className="text-[15px] font-medium">New user</SheetTitle>
        </SheetHeader>
        <form onSubmit={submit} className="flex min-h-0 flex-1 flex-col">
          <div className="flex-1 overflow-y-auto px-6 py-5">
            <div className="flex flex-col gap-5">
              <div className="flex flex-col gap-1.5">
                <Label
                  htmlFor="new-username"
                  className="text-[13px] font-medium"
                >
                  Username
                </Label>
                <div className="relative">
                  <AtSign
                    aria-hidden
                    className="pointer-events-none absolute left-3 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground/70"
                  />
                  <Input
                    id="new-username"
                    {...form.register("username")}
                    autoComplete="off"
                    spellCheck={false}
                    className="h-10 pl-9 font-mono text-[13px]"
                    placeholder="alice"
                  />
                </div>
                <FieldError
                  message={form.formState.errors.username?.message}
                />
              </div>

              <div className="flex flex-col gap-1.5">
                <Label
                  htmlFor="new-password"
                  className="text-[13px] font-medium"
                >
                  Password
                </Label>
                <div className="relative">
                  <Input
                    id="new-password"
                    {...form.register("password")}
                    type={showPassword ? "text" : "password"}
                    autoComplete="new-password"
                    className="h-10 pr-10 font-mono text-[13px]"
                    placeholder="min 8 characters"
                  />
                  <button
                    type="button"
                    onClick={() => setShowPassword((v) => !v)}
                    aria-label={showPassword ? "Hide password" : "Show password"}
                    className="absolute right-1.5 top-1/2 flex size-7 -translate-y-1/2 items-center justify-center rounded text-muted-foreground/70 transition-colors hover:text-foreground"
                    tabIndex={-1}
                  >
                    {showPassword ? (
                      <EyeOff className="size-3.5" aria-hidden />
                    ) : (
                      <Eye className="size-3.5" aria-hidden />
                    )}
                  </button>
                </div>
                <FieldError
                  message={form.formState.errors.password?.message}
                />
              </div>

              <div className="flex flex-col gap-1.5">
                <Label className="text-[13px] font-medium">Role</Label>
                <RoleField control={form.control} onChange={(r) => form.setValue("role", r, { shouldDirty: true })} />
                <p className="text-[12px] leading-[1.5] text-muted-foreground">
                  Admins can manage users, tweak settings, and reindex.
                  Regular users can search and manage their own connectors.
                </p>
              </div>
            </div>
          </div>

          <div className="flex justify-end gap-2 border-t border-border/70 bg-background/95 px-6 py-3 backdrop-blur">
            <Button
              type="button"
              variant="ghost"
              size="sm"
              onClick={() => onOpenChange(false)}
              disabled={isPending}
            >
              Cancel
            </Button>
            <Button type="submit" size="sm" disabled={isPending}>
              {isPending ? "Creating…" : "Create user"}
            </Button>
          </div>
        </form>
      </SheetContent>
    </Sheet>
  );
}

// `useWatch` is the memoizable equivalent of `form.watch("role")` — reading
// it via `form.watch` directly in JSX trips react-hooks/incompatible-library
// because watch returns a non-memoizable function. Wrapping in a subscriber
// component keeps the role value reactive without involving the form object
// outside RolePicker.
function RoleField({
  control,
  onChange,
}: {
  control: Control<NewUserValues>;
  onChange: (v: "user" | "admin") => void;
}) {
  const value = useWatch({ control, name: "role" });
  return <RolePicker value={value} onChange={onChange} />;
}

function RolePicker({
  value,
  onChange,
}: {
  value: "user" | "admin";
  onChange: (v: "user" | "admin") => void;
}) {
  const opts: {
    value: "user" | "admin";
    label: string;
    icon?: React.ReactNode;
  }[] = [
    { value: "user", label: "User" },
    {
      value: "admin",
      label: "Admin",
      icon: <ShieldCheck className="size-3.5" aria-hidden />,
    },
  ];
  return (
    <div
      role="radiogroup"
      className="inline-flex items-center gap-0.5 rounded-md border border-border bg-background p-0.5"
    >
      {opts.map((o) => {
        const active = value === o.value;
        return (
          <button
            key={o.value}
            type="button"
            role="radio"
            aria-checked={active}
            onClick={() => onChange(o.value)}
            className={cn(
              "inline-flex min-w-[72px] items-center justify-center gap-1.5 rounded-[4px] px-3 py-1.5 text-[13px] transition-colors",
              active
                ? "bg-[color-mix(in_oklch,var(--primary)_14%,transparent)] text-[color:var(--primary)]"
                : "text-muted-foreground hover:text-foreground",
            )}
          >
            {o.icon}
            {o.label}
          </button>
        );
      })}
    </div>
  );
}

function FieldError({ message }: { message?: string }) {
  if (!message) return null;
  return (
    <p className="text-[12px] leading-[1.5] text-destructive">{message}</p>
  );
}
