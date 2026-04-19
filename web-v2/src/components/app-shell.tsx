import { useState } from "react";
import { Link, useRouterState } from "@tanstack/react-router";
import { useTheme } from "next-themes";
import {
  BarChart3,
  Cable,
  ChevronUp,
  KeyRound,
  Library,
  LogOut,
  Monitor,
  Moon,
  Search,
  Settings,
  Sun,
  Users,
} from "lucide-react";

import { ChangePasswordSheet } from "@/components/admin/change-password-sheet";
import type { User } from "@/lib/api-types";
import { useLogout } from "@/hooks/use-auth";
import { useSyncJobs } from "@/hooks/use-sync-jobs";
import { useSyncStats } from "@/hooks/use-sync-stats";
import {
  SyncStrip,
  type SyncStripRunningJob,
} from "@/components/sync/sync-strip";
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarHeader,
  SidebarInset,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarProvider,
  SidebarTrigger,
} from "@/components/ui/sidebar";
import { cn } from "@/lib/utils";

type NavItem = { to: "/" | "/connectors" | "/admin/settings" | "/admin/users" | "/admin/stats"; label: string; icon: typeof Search };

const MAIN_NAV: NavItem[] = [
  { to: "/", label: "Search", icon: Search },
  { to: "/connectors", label: "Connectors", icon: Cable },
];

const ADMIN_NAV: NavItem[] = [
  { to: "/admin/settings", label: "Settings", icon: Settings },
  { to: "/admin/users", label: "Users", icon: Users },
  { to: "/admin/stats", label: "Stats", icon: BarChart3 },
];

interface AppShellProps {
  user: User;
  children: React.ReactNode;
}

export function AppShell({ user, children }: AppShellProps) {
  const routerState = useRouterState();
  const currentPath = routerState.location.pathname;
  const isAdmin = user.role === "admin";

  return (
    <SidebarProvider className="h-svh min-h-0 overflow-hidden">
      <Sidebar collapsible="icon">
        <SidebarHeader className="px-3 py-4">
          <Link
            to="/"
            className="group/masthead flex items-center gap-2.5 rounded-md px-1.5 py-1 transition-colors hover:bg-sidebar-accent/60"
          >
            <div className="flex size-7 shrink-0 items-center justify-center rounded-md bg-primary/15 text-primary">
              <Library className="size-4" aria-hidden strokeWidth={2.25} />
            </div>
            <div className="min-w-0 leading-tight group-data-[collapsible=icon]:hidden">
              <div className="text-[15px] font-semibold tracking-[-0.01em]">
                Nexus
              </div>
              <div className="text-[11px] text-muted-foreground">
                personal search
              </div>
            </div>
          </Link>
        </SidebarHeader>

        <SidebarContent className="px-2">
          <SidebarGroup className="py-1">
            <SidebarMenu>
              {MAIN_NAV.map((item) => (
                <NavRow
                  key={item.to}
                  item={item}
                  active={
                    item.to === "/"
                      ? currentPath === "/"
                      : currentPath.startsWith(item.to)
                  }
                />
              ))}
            </SidebarMenu>
          </SidebarGroup>

          {isAdmin && (
            <SidebarGroup className="py-1">
              <div className="mb-1 px-2 text-[10px] font-semibold uppercase tracking-[0.1em] text-muted-foreground/70 group-data-[collapsible=icon]:hidden">
                Admin
              </div>
              <SidebarMenu>
                {ADMIN_NAV.map((item) => (
                  <NavRow
                    key={item.to}
                    item={item}
                    active={currentPath.startsWith(item.to)}
                  />
                ))}
              </SidebarMenu>
            </SidebarGroup>
          )}
        </SidebarContent>

        <SidebarFooter className="p-2">
          <UserCard user={user} />
        </SidebarFooter>
      </Sidebar>

      <SidebarInset>
        <TopBar />
        <main className="flex min-h-0 flex-1 flex-col overflow-y-auto">
          {children}
        </main>
      </SidebarInset>
    </SidebarProvider>
  );
}

function NavRow({ item, active }: { item: NavItem; active: boolean }) {
  const Icon = item.icon;
  return (
    <SidebarMenuItem>
      <SidebarMenuButton
        render={<Link to={item.to} />}
        isActive={active}
        tooltip={item.label}
        className={cn(
          "relative h-9 gap-2.5 rounded-md px-2 text-sm transition-colors",
          "data-[active=true]:bg-primary/10 data-[active=true]:text-foreground",
          "data-[active=true]:before:absolute data-[active=true]:before:left-0 data-[active=true]:before:top-1.5 data-[active=true]:before:bottom-1.5 data-[active=true]:before:w-[2px] data-[active=true]:before:rounded-full data-[active=true]:before:bg-primary",
        )}
      >
        <Icon
          className={cn(
            "size-4 shrink-0",
            active ? "text-primary" : "text-muted-foreground",
          )}
          aria-hidden
        />
        <span className="group-data-[collapsible=icon]:hidden">
          {item.label}
        </span>
      </SidebarMenuButton>
    </SidebarMenuItem>
  );
}

function UserCard({ user }: { user: User }) {
  const [open, setOpen] = useState(false);
  const [changePasswordOpen, setChangePasswordOpen] = useState(false);
  const { theme, setTheme } = useTheme();
  const logout = useLogout();
  const initials = user.username.slice(0, 2).toUpperCase();

  return (
    <div className="relative">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
        aria-haspopup="menu"
        className={cn(
          "flex w-full items-center gap-2 rounded-md p-1.5 text-left transition-colors",
          "hover:bg-sidebar-accent",
          open && "bg-sidebar-accent",
        )}
      >
        <span
          aria-hidden
          className="flex size-7 shrink-0 items-center justify-center rounded-md bg-primary/15 text-[11px] font-semibold text-primary"
        >
          {initials}
        </span>
        <span className="flex min-w-0 flex-1 flex-col leading-tight group-data-[collapsible=icon]:hidden">
          <span className="truncate text-[13px] font-medium">
            {user.username}
          </span>
          <span className="text-[11px] text-muted-foreground">{user.role}</span>
        </span>
        <ChevronUp
          className={cn(
            "size-3.5 shrink-0 text-muted-foreground transition-transform group-data-[collapsible=icon]:hidden",
            open && "rotate-180",
          )}
          aria-hidden
        />
      </button>

      {open && (
        <>
          <button
            type="button"
            aria-label="Close menu"
            onClick={() => setOpen(false)}
            className="fixed inset-0 z-40 cursor-default"
          />
          <div
            role="menu"
            className="absolute bottom-full left-0 right-0 z-50 mb-1 overflow-hidden rounded-lg border border-border bg-popover text-popover-foreground shadow-sm"
          >
            <div className="border-b border-border/70 px-3 pb-1.5 pt-2 text-[10px] font-semibold uppercase tracking-[0.1em] text-muted-foreground/70">
              Theme
            </div>
            <ThemeRow
              label="Light"
              icon={Sun}
              active={theme === "light"}
              onClick={() => {
                setTheme("light");
                setOpen(false);
              }}
            />
            <ThemeRow
              label="Dark"
              icon={Moon}
              active={theme === "dark"}
              onClick={() => {
                setTheme("dark");
                setOpen(false);
              }}
            />
            <ThemeRow
              label="System"
              icon={Monitor}
              active={theme === "system"}
              onClick={() => {
                setTheme("system");
                setOpen(false);
              }}
            />
            <div className="border-t border-border/70">
              <button
                type="button"
                role="menuitem"
                onClick={() => {
                  setOpen(false);
                  setChangePasswordOpen(true);
                }}
                className="flex w-full items-center gap-2 px-3 py-2 text-sm text-foreground/90 transition-colors hover:bg-accent hover:text-foreground"
              >
                <KeyRound className="size-3.5 text-muted-foreground" aria-hidden />
                <span>Change password</span>
              </button>
              <button
                type="button"
                role="menuitem"
                onClick={() => {
                  setOpen(false);
                  logout();
                }}
                className="flex w-full items-center gap-2 px-3 py-2 text-sm text-foreground/90 transition-colors hover:bg-accent hover:text-foreground"
              >
                <LogOut className="size-3.5 text-muted-foreground" aria-hidden />
                <span>Sign out</span>
              </button>
            </div>
          </div>
        </>
      )}

      {changePasswordOpen && (
        <ChangePasswordSheet
          open={changePasswordOpen}
          onOpenChange={setChangePasswordOpen}
          userId={user.id}
          label="your password"
        />
      )}
    </div>
  );
}

function ThemeRow({
  label,
  icon: Icon,
  active,
  onClick,
}: {
  label: string;
  icon: typeof Sun;
  active: boolean;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      role="menuitemradio"
      aria-checked={active}
      onClick={onClick}
      className={cn(
        "flex w-full items-center gap-2 px-3 py-1.5 text-sm transition-colors",
        active
          ? "bg-primary/10 text-foreground"
          : "text-foreground/90 hover:bg-accent hover:text-foreground",
      )}
    >
      <Icon
        className={cn(
          "size-3.5",
          active ? "text-primary" : "text-muted-foreground",
        )}
        aria-hidden
      />
      <span className="flex-1 text-left">{label}</span>
      {active && (
        <span className="size-1.5 rounded-full bg-primary" aria-hidden />
      )}
    </button>
  );
}

// Live sync telegraph + mobile sidebar trigger. Stats come from the
// connectors list (source count + latest last_run); running-job state
// from the global useSyncJobs SSE subscription. Clicking the strip from
// any page lands the user on /connectors.
function TopBar() {
  const stats = useSyncStats();
  const { jobsByConnector } = useSyncJobs();

  const running: SyncStripRunningJob[] = [];
  for (const j of jobsByConnector.values()) {
    if (j.status === "running") {
      running.push({
        connectorName: j.connector_name || "connector",
        processed: j.docs_processed,
        total: j.docs_total,
      });
    }
  }

  return (
    <header className="flex h-11 shrink-0 items-center justify-between gap-3 border-b border-border px-3">
      <SidebarTrigger
        className="-ml-1 size-8 text-muted-foreground hover:text-foreground"
        aria-label="Toggle sidebar"
      />
      <SyncStrip stats={stats} running={running} totalActive={running.length} />
    </header>
  );
}
