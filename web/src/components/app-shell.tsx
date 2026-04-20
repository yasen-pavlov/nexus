import { useMemo, useState } from "react";
import { Link, useNavigate, useRouterState } from "@tanstack/react-router";
import { useTheme } from "next-themes";
import {
  BarChart3,
  Cable,
  ChevronUp,
  Library,
  LogOut,
  Monitor,
  Moon,
  Search,
  Settings,
  Sun,
  UserRound,
  Users,
} from "lucide-react";

import { CommandPalette, PaletteIcon, type PaletteItem } from "@/components/command-palette";
import { ConnectorLogo } from "@/components/connectors/connector-logo";
import { ErrorBoundary } from "@/components/error-boundary";
import { ShortcutsSheet } from "@/components/shortcuts-sheet";
import type { User } from "@/lib/api-types";
import { useConnectors } from "@/hooks/use-connectors";
import { useLogout } from "@/hooks/use-auth";
import {
  dispatchFocusSearch,
  useGlobalShortcuts,
  type ChordKey,
} from "@/hooks/use-global-shortcuts";
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

export function AppShell({ user, children }: Readonly<AppShellProps>) {
  const routerState = useRouterState();
  const currentPath = routerState.location.pathname;
  const isAdmin = user.role === "admin";

  const navigate = useNavigate();
  const { theme, setTheme } = useTheme();
  const logout = useLogout();
  const { connectors } = useConnectors();

  const [paletteOpen, setPaletteOpen] = useState(false);
  const [cheatSheetOpen, setCheatSheetOpen] = useState(false);

  // Global keyboard handlers. The chord callback navigates inline so the
  // palette doesn't sit between the keystroke and the destination.
  useGlobalShortcuts({
    onPalette: () => setPaletteOpen(true),
    onCheatSheet: () => setCheatSheetOpen(true),
    onSearchFocus: () => {
      // If we're not on the search page, jump there first; either way the
      // SearchBar's window listener picks the focus call up after mount.
      if (currentPath !== "/") {
        void navigate({ to: "/" });
      }
      // Defer one tick so the SearchBar mounts before we fire the event.
      globalThis.setTimeout(dispatchFocusSearch, 0);
    },
    onChord: (key: ChordKey) => {
      switch (key) {
        case "s":
          void navigate({ to: "/" });
          return;
        case "c":
          void navigate({ to: "/connectors" });
          return;
        case "a":
          if (isAdmin) void navigate({ to: "/admin/settings" });
          return;
      }
    },
  });

  const paletteItems = useMemo<PaletteItem[]>(() => {
    const items: PaletteItem[] = [];

    // --- Pages ---
    items.push(
      pageItem("Search", Search, "Find anything you've indexed", "g s", () =>
        navigate({ to: "/" }),
      ),
      pageItem("Connectors", Cable, "Manage data sources", "g c", () =>
        navigate({ to: "/connectors" }),
      ),
      pageItem("Account", UserRound, "Your identity and password", undefined, () =>
        navigate({ to: "/account" }),
      ),
    );
    if (isAdmin) {
      items.push(
        pageItem(
          "Admin · Settings",
          Settings,
          "Embeddings, ranking, retention",
          "g a",
          () => navigate({ to: "/admin/settings" }),
        ),
        pageItem(
          "Admin · Users",
          Users,
          "Manage accounts and roles",
          undefined,
          () => navigate({ to: "/admin/users" }),
        ),
        pageItem(
          "Admin · Stats",
          BarChart3,
          "Indexed counts and engine state",
          undefined,
          () => navigate({ to: "/admin/stats" }),
        ),
      );
    }

    // --- Connectors ---
    for (const c of connectors) {
      items.push({
        id: `connector-${c.id}`,
        group: "Connectors",
        label: c.name,
        hint: connectorHint(c.type),
        icon: <ConnectorLogo type={c.type} size="sm" />,
        keyboardHint: "Open",
        onSelect: () =>
          navigate({ to: "/connectors/$id", params: { id: c.id } }),
      });
    }

    // --- Actions ---
    const isDark = theme === "dark";
    items.push(
      {
        id: "action-toggle-theme",
        group: "Actions",
        label: isDark ? "Switch to light theme" : "Switch to dark theme",
        hint: "Light · Dark · System lives in your account menu",
        icon: (
          <PaletteIcon>
            {isDark ? <Sun className="size-4" /> : <Moon className="size-4" />}
          </PaletteIcon>
        ),
        onSelect: () => setTheme(isDark ? "light" : "dark"),
      },
      {
        id: "action-cheat-sheet",
        group: "Actions",
        label: "Show keyboard shortcuts",
        hint: "All bindings, in one dialog",
        icon: (
          <PaletteIcon>
            <Search className="size-4" />
          </PaletteIcon>
        ),
        keyboardHint: "?",
        onSelect: () => setCheatSheetOpen(true),
      },
      {
        id: "action-sign-out",
        group: "Actions",
        label: "Sign out",
        hint: "Clears your session on this browser",
        icon: (
          <PaletteIcon tone="muted">
            <LogOut className="size-4" />
          </PaletteIcon>
        ),
        onSelect: () => logout(),
      },
    );

    return items;
  }, [connectors, isAdmin, logout, navigate, setTheme, theme]);

  return (
    <SidebarProvider className="h-svh min-h-0 overflow-hidden">
      <Sidebar collapsible="icon">
        <SidebarHeader className="px-3 py-4 group-data-[collapsible=icon]:px-0">
          <Link
            to="/"
            className={cn(
              "group/masthead flex items-center gap-2.5 rounded-md px-1.5 py-1 transition-colors hover:bg-sidebar-accent/60",
              // In icon-only mode, hide the text and shrink the Link to a
              // 32×32 box centered in the sidebar — matches the body
              // NavRow tiles, and crucially keeps the hover wash hugging
              // the icon instead of running edge-to-edge across 48px.
              "group-data-[collapsible=icon]:size-8 group-data-[collapsible=icon]:mx-auto",
              "group-data-[collapsible=icon]:justify-center group-data-[collapsible=icon]:p-0",
              "group-data-[collapsible=icon]:hover:bg-sidebar-accent/40",
            )}
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

        <SidebarContent className="px-2 group-data-[collapsible=icon]:px-0">
          <SidebarGroup className="py-1">
            {/* gap-0.5 so a hover wash on the row above/below the active row
                doesn't run into the active row's marmalade fill. */}
            <SidebarMenu className="gap-0.5 group-data-[collapsible=icon]:items-center">
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
              <SidebarMenu className="gap-0.5 group-data-[collapsible=icon]:items-center">
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

        <SidebarFooter className="p-2 group-data-[collapsible=icon]:px-0 group-data-[collapsible=icon]:py-2 group-data-[collapsible=icon]:items-center">
          <UserCard user={user} />
        </SidebarFooter>
      </Sidebar>

      <SidebarInset>
        <TopBar />
        <main className="flex min-h-0 flex-1 flex-col overflow-y-auto">
          {/* key={currentPath} unmounts the boundary on route change so a
              caught error doesn't stick when the user navigates away. */}
          <ErrorBoundary key={currentPath}>{children}</ErrorBoundary>
        </main>
      </SidebarInset>

      <CommandPalette
        open={paletteOpen}
        onOpenChange={setPaletteOpen}
        items={paletteItems}
      />
      <ShortcutsSheet open={cheatSheetOpen} onOpenChange={setCheatSheetOpen} />
    </SidebarProvider>
  );
}

// --- Palette helpers --------------------------------------------------------

function pageItem(
  label: string,
  Icon: typeof Search,
  hint: string,
  keyboardHint: string | undefined,
  onSelect: () => void,
): PaletteItem {
  return {
    id: `page-${label.toLowerCase().replaceAll(/[^a-z]+/g, "-")}`,
    group: "Pages",
    label,
    hint,
    icon: (
      <PaletteIcon>
        <Icon className="size-4" />
      </PaletteIcon>
    ),
    keyboardHint,
    onSelect,
  };
}

function connectorHint(type: string): string {
  switch (type) {
    case "imap":
      return "Email · IMAP";
    case "telegram":
      return "Telegram";
    case "paperless":
      return "Paperless-ngx";
    case "filesystem":
      return "Filesystem";
    default:
      return type;
  }
}

function NavRow({ item, active }: Readonly<{ item: NavItem; active: boolean }>) {
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
          // Left primary rule reads as a "rail" on a wide expanded row;
          // on a 32px square tile it just looks like a chip stuck to one
          // side. Drop it in icon-only mode and let the marmalade fill +
          // primary-colored icon carry the active state.
          "data-[active=true]:before:absolute data-[active=true]:before:left-0 data-[active=true]:before:top-1.5 data-[active=true]:before:bottom-1.5 data-[active=true]:before:w-[2px] data-[active=true]:before:rounded-full data-[active=true]:before:bg-primary",
          "group-data-[collapsible=icon]:before:hidden",
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

function UserCard({ user }: Readonly<{ user: User }>) {
  const [open, setOpen] = useState(false);
  const { theme, setTheme } = useTheme();
  const logout = useLogout();
  const initials = user.username.slice(0, 2).toUpperCase();

  return (
    <div className="relative group-data-[collapsible=icon]:w-8">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
        aria-haspopup="menu"
        className={cn(
          "flex w-full items-center gap-2 rounded-md p-1.5 text-left transition-colors",
          "hover:bg-sidebar-accent",
          open && "bg-sidebar-accent",
          // In icon-only mode, fit the initials tile snug + center it. The
          // text + chevron are already hidden via `group-data-…hidden`, so
          // padding would otherwise leave dead space around the tile and
          // make the hover wash extend past the icon.
          "group-data-[collapsible=icon]:size-8 group-data-[collapsible=icon]:justify-center group-data-[collapsible=icon]:p-0",
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
            className={cn(
              "absolute bottom-full left-0 right-0 z-50 mb-1 overflow-hidden rounded-lg border border-border bg-popover text-popover-foreground shadow-sm",
              // In icon-only mode the trigger is a 32px square, which would
              // squash the menu down to 32px wide. Float the popover OUT to
              // the right of the sidebar instead — anchored to the
              // trigger's bottom edge, fixed width, with a small gap.
              "group-data-[collapsible=icon]:left-full group-data-[collapsible=icon]:right-auto group-data-[collapsible=icon]:bottom-0 group-data-[collapsible=icon]:mb-0 group-data-[collapsible=icon]:ml-2 group-data-[collapsible=icon]:w-56",
            )}
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
              <Link
                to="/account"
                role="menuitem"
                onClick={() => setOpen(false)}
                className="flex w-full items-center gap-2 px-3 py-2 text-sm text-foreground/90 transition-colors hover:bg-accent hover:text-foreground"
              >
                <UserRound className="size-3.5 text-muted-foreground" aria-hidden />
                <span>Account</span>
              </Link>
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
    </div>
  );
}

function ThemeRow({
  label,
  icon: Icon,
  active,
  onClick,
}: Readonly<{
  label: string;
  icon: typeof Sun;
  active: boolean;
  onClick: () => void;
}>) {
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
