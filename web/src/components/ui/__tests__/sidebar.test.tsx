import { describe, expect, it, vi, afterEach } from "vitest";
import { render, screen, userEvent, act, waitFor } from "@/test/test-utils";

import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarGroupAction,
  SidebarGroupContent,
  SidebarGroupLabel,
  SidebarHeader,
  SidebarInput,
  SidebarInset,
  SidebarMenu,
  SidebarMenuAction,
  SidebarMenuBadge,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarMenuSkeleton,
  SidebarMenuSub,
  SidebarMenuSubButton,
  SidebarMenuSubItem,
  SidebarProvider,
  SidebarRail,
  SidebarSeparator,
  SidebarTrigger,
} from "../sidebar";
import { useSidebar } from "../sidebar-context";

const originalMatchMedia = window.matchMedia;

afterEach(() => {
  window.matchMedia = originalMatchMedia;
  document.cookie = "sidebar_state=; path=/; max-age=0";
});

function mockMobile(mobile: boolean) {
  window.matchMedia = vi.fn().mockReturnValue({
    matches: mobile,
    media: "",
    onchange: null,
    addListener: vi.fn(),
    removeListener: vi.fn(),
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    dispatchEvent: vi.fn(),
  }) as unknown as typeof window.matchMedia;
}

describe("SidebarProvider", () => {
  it("starts expanded and collapses when the trigger is clicked", async () => {
    mockMobile(false);
    render(
      <SidebarProvider>
        <Sidebar>
          <SidebarHeader>header</SidebarHeader>
        </Sidebar>
        <SidebarInset>
          <SidebarTrigger />
          <span data-testid="probe">ok</span>
        </SidebarInset>
      </SidebarProvider>,
    );
    // data-state reflects provider state — starts expanded by default.
    const wrapper = document.querySelector('[data-state="expanded"]');
    expect(wrapper).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: /toggle sidebar/i }));
    await waitFor(() =>
      expect(document.querySelector('[data-state="collapsed"]')).toBeTruthy(),
    );
    // Collapse should persist the cookie so reloads restore state.
    expect(document.cookie).toContain("sidebar_state=false");
  });

  it("keyboard shortcut (Ctrl+B) toggles the sidebar", async () => {
    mockMobile(false);
    render(
      <SidebarProvider>
        <Sidebar>
          <SidebarContent>c</SidebarContent>
        </Sidebar>
      </SidebarProvider>,
    );
    expect(document.querySelector('[data-state="expanded"]')).toBeTruthy();
    await act(async () => {
      window.dispatchEvent(
        new KeyboardEvent("keydown", { key: "b", ctrlKey: true }),
      );
    });
    await waitFor(() =>
      expect(document.querySelector('[data-state="collapsed"]')).toBeTruthy(),
    );
  });

  it("renders the mobile Sheet variant and opens it via the trigger", async () => {
    mockMobile(true);
    render(
      <SidebarProvider>
        <Sidebar>
          <SidebarContent>mobile sidebar</SidebarContent>
        </Sidebar>
        <SidebarInset>
          <SidebarTrigger />
        </SidebarInset>
      </SidebarProvider>,
    );
    await userEvent.click(screen.getByRole("button", { name: /toggle sidebar/i }));
    // The Sheet portal re-renders the sidebar content when opened on mobile.
    await waitFor(() =>
      expect(screen.getAllByText(/mobile sidebar/)[0]).toBeInTheDocument(),
    );
  });

  it("respects controlled `open` + `onOpenChange`", async () => {
    mockMobile(false);
    const onOpenChange = vi.fn();
    render(
      <SidebarProvider open={true} onOpenChange={onOpenChange}>
        <Sidebar>
          <SidebarContent>c</SidebarContent>
        </Sidebar>
        <SidebarInset>
          <SidebarTrigger />
        </SidebarInset>
      </SidebarProvider>,
    );
    await userEvent.click(screen.getByRole("button", { name: /toggle sidebar/i }));
    expect(onOpenChange).toHaveBeenCalledWith(false);
  });
});

describe("Sidebar collapsible variants", () => {
  it("renders collapsible=none as a plain div", () => {
    mockMobile(false);
    render(
      <SidebarProvider>
        <Sidebar collapsible="none" data-testid="bar">
          <SidebarContent>plain</SidebarContent>
        </Sidebar>
      </SidebarProvider>,
    );
    expect(screen.getByTestId("bar")).toBeInTheDocument();
    expect(screen.getByText("plain")).toBeInTheDocument();
  });
});

describe("Sidebar sub-components render", () => {
  it("mounts the full vocabulary without throwing", () => {
    mockMobile(false);
    render(
      <SidebarProvider>
        <Sidebar>
          <SidebarHeader>h</SidebarHeader>
          <SidebarRail />
          <SidebarContent>
            <SidebarGroup>
              <SidebarGroupLabel>label</SidebarGroupLabel>
              <SidebarGroupAction>+</SidebarGroupAction>
              <SidebarGroupContent>
                <SidebarInput placeholder="filter" />
                <SidebarSeparator />
                <SidebarMenu>
                  <SidebarMenuItem>
                    <SidebarMenuButton>item</SidebarMenuButton>
                    <SidebarMenuAction>act</SidebarMenuAction>
                    <SidebarMenuBadge>3</SidebarMenuBadge>
                  </SidebarMenuItem>
                  <SidebarMenuItem>
                    <SidebarMenuSkeleton />
                  </SidebarMenuItem>
                  <SidebarMenuItem>
                    <SidebarMenuSub>
                      <SidebarMenuSubItem>
                        <SidebarMenuSubButton>sub</SidebarMenuSubButton>
                      </SidebarMenuSubItem>
                    </SidebarMenuSub>
                  </SidebarMenuItem>
                </SidebarMenu>
              </SidebarGroupContent>
            </SidebarGroup>
          </SidebarContent>
          <SidebarFooter>f</SidebarFooter>
        </Sidebar>
      </SidebarProvider>,
    );
    expect(screen.getByText("label")).toBeInTheDocument();
    expect(screen.getByText("item")).toBeInTheDocument();
    expect(screen.getByText("sub")).toBeInTheDocument();
    expect(screen.getByPlaceholderText("filter")).toBeInTheDocument();
  });
});

describe("useSidebar", () => {
  it("throws when used outside SidebarProvider", () => {
    function Probe() {
      useSidebar();
      return null;
    }
    const spy = vi.spyOn(console, "error").mockImplementation(() => {});
    expect(() => render(<Probe />)).toThrow(
      /useSidebar must be used within a SidebarProvider/,
    );
    spy.mockRestore();
  });
});
