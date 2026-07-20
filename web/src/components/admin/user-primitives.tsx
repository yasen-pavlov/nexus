import { KeyRound, ShieldCheck, Trash2 } from "lucide-react";

import {
  DropdownMenuItem,
  DropdownMenuSeparator,
} from "@/components/ui/dropdown-menu";
import { cn } from "@/lib/utils";

import type { AdminUserRow } from "@/hooks/use-users";

/**
 * Presentational primitives shared by the desktop users table and the mobile
 * card stack, so the role badge, "you" pill, initials tile, and the actions
 * menu stay identical across both layouts instead of drifting as two copies.
 */

/** Monogram tile. `sm` (28px) suits the dense table/self rows, `md` (36px) the
 *  roomier mobile cards. */
export function InitialsTile({
  username,
  size = "sm",
}: Readonly<{ username: string; size?: "sm" | "md" }>) {
  const initials = username.slice(0, 2).toUpperCase();
  return (
    <span
      aria-hidden
      className={cn(
        "flex shrink-0 items-center justify-center rounded-md bg-primary/15 font-semibold text-primary",
        size === "md" ? "size-9 text-[12px]" : "size-7 text-[11px]",
      )}
    >
      {initials}
    </span>
  );
}

export function YouBadge() {
  return (
    <span className="rounded-full bg-primary/15 px-1.5 py-0.5 text-[10px] font-semibold uppercase tracking-[0.08em] text-primary">
      you
    </span>
  );
}

export function RoleBadge({ role }: Readonly<{ role: "admin" | "user" }>) {
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
      />{" "}
      user
    </span>
  );
}

/**
 * The two per-user dropdown actions (change password, guarded self-delete),
 * rendered inside a DropdownMenuContent by both layouts.
 */
export function UserActionMenuItems({
  user,
  isSelf,
  onChangePassword,
  onDelete,
}: Readonly<{
  user: AdminUserRow;
  isSelf: boolean;
  onChangePassword: (u: AdminUserRow) => void;
  onDelete: (u: AdminUserRow) => void;
}>) {
  return (
    <>
      {/* onClick (not onSelect) — base-ui Menu.Item's onSelect swallows/defers
          the click in a way that leaves the follow-up Sheet/Dialog never
          mounting. Matches the connector-card pattern that already works. */}
      <DropdownMenuItem onClick={() => onChangePassword(user)} className="gap-2">
        <KeyRound className="size-3.5" aria-hidden />
        Change password
      </DropdownMenuItem>
      <DropdownMenuSeparator />
      <DropdownMenuItem
        disabled={isSelf}
        onClick={() => {
          if (isSelf) return;
          onDelete(user);
        }}
        className={cn(
          "gap-2 text-destructive focus:text-destructive",
          isSelf && "cursor-not-allowed opacity-50",
        )}
      >
        <Trash2 className="size-3.5" aria-hidden />
        {isSelf ? "Can't delete yourself" : "Delete account"}
      </DropdownMenuItem>
    </>
  );
}
