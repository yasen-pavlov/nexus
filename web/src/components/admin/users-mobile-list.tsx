import { KeyRound, MoreHorizontal, ShieldCheck, Trash2 } from "lucide-react";

import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { formatAbsolute, formatRelative } from "@/lib/format";
import { cn } from "@/lib/utils";

import type { AdminUserRow } from "@/hooks/use-users";

export interface UsersMobileListProps {
  rows: AdminUserRow[];
  currentUserId: string;
  onChangePassword: (u: AdminUserRow) => void;
  onDelete: (u: AdminUserRow) => void;
}

/**
 * Mobile card-stack variant of the users table. Each user gets a card
 * with the same primitives as the desktop row (initials tile, role
 * badge, "you" pill, actions menu) but laid out vertically so the
 * narrow viewport doesn't squish columns.
 */
export function UsersMobileList({
  rows,
  currentUserId,
  onChangePassword,
  onDelete,
}: Readonly<UsersMobileListProps>) {
  return (
    <div className="flex flex-col gap-2">
      {rows.map((u) => {
        const isSelf = u.id === currentUserId;
        return (
          <article
            key={u.id}
            className="rounded-lg border border-border bg-card p-3"
          >
            <div className="flex items-start gap-3">
              <InitialsTile username={u.username} />
              <div className="min-w-0 flex-1 leading-tight">
                <div className="flex flex-wrap items-center gap-1.5">
                  <span className="truncate text-[15px] font-medium text-foreground">
                    {u.username}
                  </span>
                  {isSelf && <YouBadge />}
                </div>
                <div className="mt-1.5 flex items-center gap-2">
                  <RoleBadge role={u.role} />
                  {u.created_at && (
                    <span
                      title={formatAbsolute(u.created_at)}
                      className="text-[11.5px] tabular-nums text-muted-foreground"
                    >
                      since {formatRelative(u.created_at)}
                    </span>
                  )}
                </div>
              </div>
              <DropdownMenu>
                <DropdownMenuTrigger
                  aria-label={`Actions for ${u.username}`}
                  className="-mr-1 inline-flex size-8 shrink-0 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
                >
                  <MoreHorizontal className="size-4" aria-hidden />
                </DropdownMenuTrigger>
                <DropdownMenuContent align="end" className="w-48">
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
          </article>
        );
      })}
    </div>
  );
}

function InitialsTile({ username }: Readonly<{ username: string }>) {
  const initials = username.slice(0, 2).toUpperCase();
  return (
    <span
      aria-hidden
      className="flex size-9 shrink-0 items-center justify-center rounded-md bg-primary/15 text-[12px] font-semibold text-primary"
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

function RoleBadge({ role }: Readonly<{ role: "admin" | "user" }>) {
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
