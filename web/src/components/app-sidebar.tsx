import {
  Eye,
  LayoutDashboard,
  MessageSquareText,
  MessagesSquare,
  Settings,
  Users,
} from "lucide-react"
import { NavLink, useLocation } from "react-router"

import { useMe } from "@/api/auth"
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
      {/* Session summary; AUTH-4 adds the logout control here. */}
      {me && (
        <SidebarFooter>
          <div className="flex flex-col gap-0.5 px-2 py-1.5 text-xs">
            <span className="truncate font-medium">{me.tenant.name}</span>
            <span className="truncate text-muted-foreground">
              {me.user.email}
            </span>
          </div>
        </SidebarFooter>
      )}
    </Sidebar>
  )
}
