import {
  Building2,
  CreditCard,
  FileSearch,
  Newspaper,
  LogOut,
  MessageSquareText,
  Settings,
  Users,
  Lightbulb,
  ClipboardCheck,
  Plus,
  Check,
  ChevronDown,
} from "lucide-react"
import { useMutation } from "@connectrpc/connect-query"
import { useQueryClient } from "@tanstack/react-query"
import { NavLink, useLocation, useNavigate } from "react-router"

import { useAccountContext, useMe } from "@/api/hooks"
import { AccountRole } from "@/gen/opensight/v1/account_pb"
import { Access, BusinessStatus } from "@/gen/opensight/v1/common_pb"
import { accountPath } from "@/lib/account-path"
import { isCompedAccountAdmin } from "@/lib/operator"
import { logout } from "@/gen/opensight/v1/auth-AuthService_connectquery"
import { Button } from "@/components/ui/button"
import { Logo } from "@/components/logo"
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarGroupContent,
  SidebarGroupLabel,
  SidebarHeader,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
} from "@/components/ui/sidebar"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"

const businessSections = [
  { title: "Brief", to: "/overview", icon: Newspaper },
  { title: "Questions", to: "/prompts", icon: MessageSquareText },
  { title: "Competitors", to: "/competitors", icon: Users },
  { title: "Monitoring history", to: "/runs", icon: FileSearch },
]

// "Settings", not "Business profile": the page also carries the monitoring
// pause switch and plan, so the narrower label sent people looking elsewhere.
const businessSettings = [{ title: "Settings", to: "/setup", icon: Settings }]

const improveSections = [
  { title: "Next actions", to: "/improve/actions", icon: Lightbulb },
  {
    title: "Visibility checklist",
    to: "/improve/checklist",
    icon: ClipboardCheck,
  },
]

const workspaceSections = [{ title: "Members", to: "/team", icon: Users }]

export function AppSidebar() {
  const { pathname } = useLocation()
  const { data: me } = useMe()
  const { data: account } = useAccountContext()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const logoutMutation = useMutation(logout, {
    onSettled: () => {
      queryClient.clear()
      navigate("/login", { replace: true })
    },
  })
  const slug = account?.account?.slug ?? ""
  const business = account?.businesses.find(
    (candidate) => candidate.status !== BusinessStatus.DRAFT
  )
  const contextName =
    business?.name ?? account?.account?.name ?? "Your workspace"
  const contextLabel = business ? "Business" : "Workspace"
  const hasBusinessNavigation =
    business !== undefined && account?.access !== Access.NEVER
  const hasMultipleWorkspaces = (me?.memberships.length ?? 0) > 1
  const canCreateCompedWorkspace = isCompedAccountAdmin(me?.user?.email)
  const workspaceItems = (me?.memberships ?? []).flatMap((membership) =>
    membership.account
      ? [{ value: membership.account.slug, label: membership.account.name }]
      : []
  )
  const showWorkspaceSelector =
    me !== undefined &&
    account?.account !== undefined &&
    (hasMultipleWorkspaces || canCreateCompedWorkspace)
  const visibleWorkspaceSections =
    account?.role === AccountRole.OWNER
      ? [
          ...workspaceSections,
          { title: "Billing", to: "/billing", icon: CreditCard },
        ]
      : workspaceSections

  function switchAccount(nextSlug: string) {
    if (nextSlug === slug) return
    queryClient.clear()
    navigate(accountPath(nextSlug))
  }

  return (
    <Sidebar>
      <SidebarHeader>
        <div className="flex items-center gap-2 px-2 py-1.5">
          <Logo className="size-5" />
          <span className="font-heading text-base font-semibold tracking-tight">
            OpenSight
          </span>
        </div>
        {showWorkspaceSelector ? (
          <DropdownMenu>
            <DropdownMenuTrigger
              className="flex w-full items-center gap-3 rounded-xl bg-transparent px-2 py-2 text-sidebar-foreground transition-colors outline-none hover:bg-sidebar-accent focus-visible:ring-3 focus-visible:ring-sidebar-ring data-popup-open:bg-sidebar-accent"
              aria-label="Switch workspace"
            >
              <WorkspaceSummary label={contextLabel} name={contextName} />
              <ChevronDown className="size-4 shrink-0 text-muted-foreground" />
            </DropdownMenuTrigger>
            <DropdownMenuContent>
              {workspaceItems.map((item) => (
                <DropdownMenuItem
                  key={item.value}
                  onClick={() => switchAccount(item.value)}
                >
                  <span className="min-w-0 flex-1 truncate">{item.label}</span>
                  {item.value === slug && <Check />}
                </DropdownMenuItem>
              ))}
              {canCreateCompedWorkspace && (
                <>
                  <DropdownMenuSeparator />
                  <DropdownMenuItem onClick={() => navigate("/accounts/new")}>
                    <Plus />
                    Create workspace
                  </DropdownMenuItem>
                </>
              )}
            </DropdownMenuContent>
          </DropdownMenu>
        ) : (
          <div className="flex items-center gap-3 rounded-xl px-2 py-2">
            <WorkspaceSummary label={contextLabel} name={contextName} />
          </div>
        )}
      </SidebarHeader>
      <SidebarContent>
        {hasBusinessNavigation && (
          <>
            <SidebarGroup>
              <SidebarGroupLabel>Monitor</SidebarGroupLabel>
              <SidebarGroupContent>
                <SidebarMenu>
                  {businessSections.map((section) => (
                    <SidebarMenuItem key={section.to}>
                      <SidebarMenuButton
                        isActive={pathname.startsWith(
                          accountPath(slug, section.to)
                        )}
                        tooltip={section.title}
                        render={<NavLink to={accountPath(slug, section.to)} />}
                      >
                        <section.icon />
                        <span>{section.title}</span>
                      </SidebarMenuButton>
                    </SidebarMenuItem>
                  ))}
                </SidebarMenu>
              </SidebarGroupContent>
            </SidebarGroup>
            <SidebarGroup>
              <SidebarGroupLabel>Improve</SidebarGroupLabel>
              <SidebarGroupContent>
                <SidebarMenu>
                  {improveSections.map((section) => (
                    <SidebarMenuItem key={section.to}>
                      <SidebarMenuButton
                        isActive={pathname.startsWith(
                          accountPath(slug, section.to)
                        )}
                        tooltip={section.title}
                        render={<NavLink to={accountPath(slug, section.to)} />}
                      >
                        <section.icon />
                        <span>{section.title}</span>
                      </SidebarMenuButton>
                    </SidebarMenuItem>
                  ))}
                </SidebarMenu>
              </SidebarGroupContent>
            </SidebarGroup>
            <SidebarGroup>
              <SidebarGroupLabel>Business</SidebarGroupLabel>
              <SidebarGroupContent>
                <SidebarMenu>
                  {businessSettings.map((section) => (
                    <SidebarMenuItem key={section.to}>
                      <SidebarMenuButton
                        isActive={pathname.startsWith(
                          accountPath(slug, section.to)
                        )}
                        tooltip={section.title}
                        render={<NavLink to={accountPath(slug, section.to)} />}
                      >
                        <section.icon />
                        <span>{section.title}</span>
                      </SidebarMenuButton>
                    </SidebarMenuItem>
                  ))}
                </SidebarMenu>
              </SidebarGroupContent>
            </SidebarGroup>
          </>
        )}
        <SidebarGroup>
          <SidebarGroupLabel>Workspace</SidebarGroupLabel>
          <SidebarGroupContent>
            <SidebarMenu>
              {visibleWorkspaceSections.map((section) => (
                <SidebarMenuItem key={section.to}>
                  <SidebarMenuButton
                    isActive={pathname.startsWith(
                      accountPath(slug, section.to)
                    )}
                    tooltip={section.title}
                    render={<NavLink to={accountPath(slug, section.to)} />}
                  >
                    <section.icon />
                    <span>{section.title}</span>
                  </SidebarMenuButton>
                </SidebarMenuItem>
              ))}
            </SidebarMenu>
          </SidebarGroupContent>
        </SidebarGroup>
      </SidebarContent>
      {me && (
        <SidebarFooter>
          <div className="flex items-center gap-2 px-2 py-1.5">
            <div className="min-w-0 flex-1 text-xs">
              <div className="truncate text-muted-foreground">Signed in as</div>
              <div className="truncate font-medium">{me.user?.email}</div>
            </div>
            <Button
              variant="ghost"
              size="icon-sm"
              aria-label="Log out"
              title="Log out"
              disabled={logoutMutation.isPending}
              onClick={() => logoutMutation.mutate({})}
            >
              <LogOut />
            </Button>
          </div>
        </SidebarFooter>
      )}
    </Sidebar>
  )
}

function WorkspaceSummary({ label, name }: { label: string; name: string }) {
  return (
    <>
      <div className="flex size-8 shrink-0 items-center justify-center rounded-lg bg-sidebar-accent text-sidebar-accent-foreground">
        <Building2 className="size-4" />
      </div>
      <div className="min-w-0 flex-1 text-left">
        <div className="text-xs text-muted-foreground">{label}</div>
        <div className="truncate text-sm font-medium">{name}</div>
      </div>
    </>
  )
}
