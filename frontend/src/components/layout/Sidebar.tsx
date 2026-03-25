import type { ElementType } from 'react'
import { NavLink } from 'react-router-dom'
import { useTranslation } from 'react-i18next'

export interface SidebarNavItem {
  to: string
  icon: ElementType
  labelKey: string
  badge?: number
}

interface SidebarProps {
  navItems: SidebarNavItem[]
}

export function Sidebar({ navItems }: SidebarProps) {
  const { t } = useTranslation()

  return (
    <aside className="w-52 bg-muted/50 border-r border-border flex flex-col py-4 shrink-0">
      <div className="px-4 mb-3">
        <span className="text-[10px] font-semibold uppercase tracking-widest text-muted-foreground">
          Administration
        </span>
      </div>
      <nav className="flex flex-col gap-0.5 px-2">
        {navItems.map((item) => (
          <NavLink
            key={item.to}
            to={item.to}
            end={item.to === '/'}
            className={({ isActive }) =>
              `flex items-center gap-3 px-3 py-2 rounded-md text-sm font-medium transition-all relative ${
                isActive
                  ? 'bg-primary/10 text-primary'
                  : 'text-muted-foreground hover:bg-accent hover:text-foreground'
              }`
            }
          >
            {({ isActive }) => (
              <>
                {isActive && (
                  <div className="absolute left-0 top-1/2 -translate-y-1/2 w-0.5 h-5 bg-primary rounded-r" />
                )}
                <item.icon className="w-4 h-4 shrink-0" />
                <span className="flex-1">{t(item.labelKey)}</span>
                {item.badge != null && item.badge > 0 && (
                  <span className="text-[10px] font-semibold bg-quarantine text-quarantine-foreground px-1.5 py-0.5 rounded-full min-w-[18px] text-center">
                    {item.badge}
                  </span>
                )}
              </>
            )}
          </NavLink>
        ))}
      </nav>
    </aside>
  )
}
