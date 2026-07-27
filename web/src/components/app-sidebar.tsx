import {
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

import { useMe } from "@/api/hooks"
import { logout } from "@/gen/opensight/v1/auth-AuthService_connectquery"
import { Button } from "@/components/ui/button"
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarGroupContent,
  SidebarHeader,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
} from "@/components/ui/sidebar"

const sections = [
  { title: "Brief", to: "/overview", icon: Newspaper },
  { title: "Questions", to: "/prompts", icon: MessageSquareText },
  { title: "Competitors", to: "/competitors", icon: Users },
  { title: "Monitoring history", to: "/runs", icon: FileSearch },
  { title: "Settings", to: "/setup", icon: Settings },
]

export function AppSidebar() {
  const { pathname } = useLocation()
  const { data: me } = useMe()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const logoutMutation = useMutation(logout, {
    onSettled: () => {
      queryClient.clear()
      navigate("/login", { replace: true })
    },
  })

  return (
    <Sidebar>
      <SidebarHeader>
        <div className="flex items-center gap-2 px-2 py-1.5">
          <span
            className="bg-primary relative grid size-6 place-items-center overflow-hidden rounded-md"
            aria-hidden="true"
          >
            <span className="size-2.5 rounded-full border-2 border-primary-foreground" />
            <span className="bg-marker absolute right-1 bottom-1 size-1 rounded-full" />
          </span>
          <span className="font-heading text-base font-semibold tracking-tight">
            OpenSight
          </span>
        </div>
      </SidebarHeader>
      <SidebarContent>
        <SidebarGroup>
          <SidebarGroupContent>
            <SidebarMenu>
              {sections.map((section) => (
                <SidebarMenuItem key={section.to}>
                  <SidebarMenuButton
                    isActive={pathname.startsWith(section.to)}
                    tooltip={section.title}
                    render={<NavLink to={section.to} />}
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
              <div className="truncate font-medium">{me.tenant?.name}</div>
              <div className="truncate text-muted-foreground">
                {me.user?.email}
              </div>
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
