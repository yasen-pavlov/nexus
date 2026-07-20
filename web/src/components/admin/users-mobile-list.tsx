import { MoreHorizontal } from "lucide-react";

import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import {
  InitialsTile,
  RoleBadge,
  UserActionMenuItems,
  YouBadge,
} from "@/components/admin/user-primitives";
import { formatAbsolute, formatRelative } from "@/lib/format";

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
              <InitialsTile username={u.username} size="md" />
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
                  <UserActionMenuItems
                    user={u}
                    isSelf={isSelf}
                    onChangePassword={onChangePassword}
                    onDelete={onDelete}
                  />
                </DropdownMenuContent>
              </DropdownMenu>
            </div>
          </article>
        );
      })}
    </div>
  );
}
