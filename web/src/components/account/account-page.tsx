import { useState } from "react";
import { KeyRound, LogOut, ShieldCheck } from "lucide-react";

import { Button } from "@/components/ui/button";
import { ChangePasswordSheet } from "@/components/admin/change-password-sheet";
import type { User } from "@/lib/api-types";
import { useLogout } from "@/hooks/use-auth";
import { formatAbsolute, formatRelative } from "@/lib/format";

export interface AccountPageProps {
  user: User;
}

/**
 * /account — self-service surface for the signed-in user. Identity card
 * with initials tile + role + member-since, plus the two reachable
 * actions (change password, sign out). No backend writes for username
 * by design — this is a homelab tool with a single-digit user count.
 */
export function AccountPage({ user }: AccountPageProps) {
  const [pwOpen, setPwOpen] = useState(false);
  const logout = useLogout();
  const initials = user.username.slice(0, 2).toUpperCase();

  return (
    <div className="mx-auto w-full max-w-2xl flex-1 px-6 py-8">
      <header className="mb-8">
        <div className="text-[10px] font-semibold uppercase tracking-[0.1em] text-muted-foreground/80">
          You
        </div>
        <h1 className="mt-1 text-[20px] font-medium tracking-[-0.005em] text-foreground">
          Account
        </h1>
        <p className="mt-1 text-[13.5px] leading-[1.55] text-muted-foreground">
          Identity, password, and the way out. Nothing else needs your
          attention here right now.
        </p>
      </header>

      <section className="overflow-hidden rounded-lg border border-border bg-card">
        {/* Identity strip — large initials, name, badge, member-since */}
        <div className="flex flex-col gap-4 p-5 sm:flex-row sm:items-center sm:gap-5">
          <span
            aria-hidden
            className="flex size-14 shrink-0 items-center justify-center rounded-xl bg-primary/15 text-[16px] font-semibold tracking-[-0.005em] text-primary"
          >
            {initials}
          </span>
          <div className="min-w-0 flex-1 leading-tight">
            <div className="flex flex-wrap items-center gap-2">
              <span className="truncate text-[18px] font-medium tracking-[-0.005em] text-foreground">
                {user.username}
              </span>
              <RoleBadge role={user.role} />
            </div>
            <div className="mt-1.5 text-[12.5px] text-muted-foreground">
              Member{" "}
              <span
                title={formatAbsolute(user.created_at)}
                className="text-foreground/80"
              >
                {formatRelative(user.created_at)}
              </span>
            </div>
          </div>
        </div>

        {/* Action rail — two rows, separated by hairline */}
        <div className="border-t border-border/70">
          <ActionRow
            icon={<KeyRound className="size-4" />}
            label="Change password"
            hint="Rotate your password. You stay signed in after saving."
            action={
              <Button
                type="button"
                size="sm"
                variant="outline"
                onClick={() => setPwOpen(true)}
              >
                Change…
              </Button>
            }
          />
          <ActionRow
            icon={<LogOut className="size-4" />}
            label="Sign out"
            hint="Clears your session on this browser only."
            action={
              <Button
                type="button"
                size="sm"
                variant="ghost"
                onClick={() => logout()}
                className="text-destructive hover:bg-destructive/10 hover:text-destructive"
              >
                Sign out
              </Button>
            }
          />
        </div>
      </section>

      {pwOpen && (
        <ChangePasswordSheet
          open={pwOpen}
          onOpenChange={setPwOpen}
          userId={user.id}
          label="your password"
        />
      )}
    </div>
  );
}

function ActionRow({
  icon,
  label,
  hint,
  action,
}: {
  icon: React.ReactNode;
  label: string;
  hint: string;
  action: React.ReactNode;
}) {
  return (
    <div className="flex items-center gap-3 border-t border-border/40 px-5 py-3.5 first:border-t-0">
      <span
        aria-hidden
        className="flex size-8 shrink-0 items-center justify-center rounded-md bg-muted text-muted-foreground"
      >
        {icon}
      </span>
      <div className="min-w-0 flex-1 leading-tight">
        <div className="text-[13.5px] font-medium text-foreground">{label}</div>
        <div className="mt-0.5 text-[12px] text-muted-foreground">{hint}</div>
      </div>
      {action}
    </div>
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
