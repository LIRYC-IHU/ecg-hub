import { Search, Heart, ListFilter, ShieldCheck, Users } from 'lucide-react'

type EmptyStateType = 'patients' | 'ecg' | 'audit' | 'quarantine' | 'users'

interface EmptyStateProps {
  type: EmptyStateType
  message?: string
}

const configs: Record<EmptyStateType, { icon: React.ElementType; text: string; color: string }> = {
  patients:   { icon: Search,      text: 'Aucun patient trouvé',               color: 'text-muted-foreground' },
  ecg:        { icon: Heart,       text: 'Aucun ECG pour ce patient',           color: 'text-muted-foreground' },
  audit:      { icon: ListFilter,  text: 'Aucune entrée dans cette période',    color: 'text-muted-foreground' },
  quarantine: { icon: ShieldCheck, text: 'Aucun fichier en quarantaine ✓',      color: 'text-success' },
  users:      { icon: Users,       text: 'Aucun utilisateur connecté',          color: 'text-muted-foreground' },
}

export function EmptyState({ type, message }: EmptyStateProps) {
  const { icon: Icon, text, color } = configs[type]
  return (
    <div className="flex flex-col items-center justify-center py-16 gap-3">
      <div className="w-12 h-12 rounded-full bg-muted flex items-center justify-center">
        <Icon className={`w-6 h-6 ${color}`} />
      </div>
      <p className={`text-sm font-medium ${color}`}>{message ?? text}</p>
    </div>
  )
}
