import {
  Building2,
  CreditCard,
  FileSearch,
  Newspaper,
  LogOut,
  MessageSquareText,
  Settings,
  Users,
} from "lucide-react"
import { useMutation } from "@connectrpc/connect-query"
import { useQueryClient } from "@tanstack/react-query"
import { NavLink, useLocation, useNavigate } from "react-router"

import { useAccountContext, useMe } from "@/api/hooks"
import { AccountRole } from "@/gen/opensight/v1/account_pb"
import { Access, BusinessStatus } from "@/gen/opensight/v1/common_pb"
import { accountPath } from "@/lib/account-path"
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
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"

const businessSections = [
  { title: "Brief", to: "/overview", icon: Newspaper },
  { title: "Questions", to: "/prompts", icon: MessageSquareText },
  { title: "Competitors", to: "/competitors", icon: Users },
  { title: "Monitoring history", to: "/runs", icon: FileSearch },
]

const businessSettings = [
  { title: "Business profile", to: "/setup", icon: Settings },
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
  const visibleWorkspaceSections =
    account?.role === AccountRole.OWNER
      ? [
          ...workspaceSections,
          { title: "Billing", to: "/billing", icon: CreditCard },
        ]
      : workspaceSections

  function switchAccount(nextSlug: string | null) {
    if (!nextSlug || nextSlug === slug) return
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
        <div className="flex items-center gap-3 rounded-xl px-2 py-2">
          <div className="flex size-8 shrink-0 items-center justify-center rounded-lg bg-sidebar-accent text-sidebar-accent-foreground">
            <Building2 className="size-4" />
          </div>
          <div className="min-w-0">
            <div className="text-xs text-muted-foreground">{contextLabel}</div>
            <div className="truncate text-sm font-medium">{contextName}</div>
          </div>
        </div>
        {me && account?.account && hasMultipleWorkspaces && (
          <div className="flex flex-col gap-1 px-1">
            <span className="px-2 text-xs text-muted-foreground">
              Workspace
            </span>
            <Select value={slug} onValueChange={switchAccount}>
              <SelectTrigger className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {me.memberships.map(
                  (membership) =>
                    membership.account && (
                      <SelectItem
                        key={membership.account.id}
                        value={membership.account.slug}
                      >
                        {membership.account.name}
                      </SelectItem>
                    )
                )}
              </SelectContent>
            </Select>
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
