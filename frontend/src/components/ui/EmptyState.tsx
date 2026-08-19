import { Search, Heart, ListFilter, ShieldCheck, Users } from 'lucide-react'
import { useTranslation } from 'react-i18next'

type EmptyStateType = 'patients' | 'ecg' | 'audit' | 'quarantine' | 'users'

interface EmptyStateProps {
  type: EmptyStateType
  message?: string
}

const configs: Record<EmptyStateType, { icon: React.ElementType; labelKey: string; color: string }> = {
  patients:   { icon: Search,      labelKey: 'empty.patients',   color: 'text-muted-foreground' },
  ecg:        { icon: Heart,       labelKey: 'empty.ecg',        color: 'text-muted-foreground' },
  audit:      { icon: ListFilter,  labelKey: 'empty.audit',      color: 'text-muted-foreground' },
  quarantine: { icon: ShieldCheck, labelKey: 'empty.quarantine', color: 'text-success' },
  users:      { icon: Users,       labelKey: 'empty.users',      color: 'text-muted-foreground' },
}

export function EmptyState({ type, message }: EmptyStateProps) {
  const { t } = useTranslation()
  const { icon: Icon, labelKey, color } = configs[type]
  return (
    <div className="flex flex-col items-center justify-center py-16 gap-3">
      <div className="w-12 h-12 rounded-full bg-muted flex items-center justify-center">
        <Icon className={`w-6 h-6 ${color}`} />
      </div>
      <p className={`text-sm font-medium ${color}`}>{message ?? t(labelKey)}</p>
    </div>
  )
}
