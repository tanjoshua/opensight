import {
  Eye,
  LayoutDashboard,
  LogOut,
  MessageSquareText,
  MessagesSquare,
  Settings,
  Users,
} from "lucide-react"
import { useMutation, useQueryClient } from "@tanstack/react-query"
import { NavLink, useLocation, useNavigate } from "react-router"

import { logout, useMe } from "@/api/auth"
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
  { title: "Overview", to: "/overview", icon: LayoutDashboard },
  { title: "Prompts", to: "/prompts", icon: MessageSquareText },
  { title: "Competitors", to: "/competitors", icon: Users },
  { title: "Responses", to: "/responses", icon: MessagesSquare },
  { title: "Setup", to: "/setup", icon: Settings },
]

export function AppSidebar() {
  const { pathname } = useLocation()
  const { data: me } = useMe()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const logoutMutation = useMutation({
    mutationFn: logout,
    onSettled: () => {
      queryClient.clear()
      navigate("/login", { replace: true })
    },
  })

  return (
    <Sidebar>
      <SidebarHeader>
        <div className="flex items-center gap-2 px-2 py-1.5">
          <Eye className="size-5" />
          <span className="font-heading text-base font-semibold">
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
              <div className="truncate font-medium">{me.tenant.name}</div>
              <div className="truncate text-muted-foreground">
                {me.user.email}
              </div>
            </div>
            <Button
              variant="ghost"
              size="icon-sm"
              aria-label="Log out"
              title="Log out"
              disabled={logoutMutation.isPending}
              onClick={() => logoutMutation.mutate()}
            >
              <LogOut />
            </Button>
          </div>
        </SidebarFooter>
      )}
    </Sidebar>
  )
}
