import type { ECG } from '../../types'
import { useTranslation } from 'react-i18next'

interface Props {
  status: ECG['hl7_status']
}

// 8px colored dot per UX spec (Direction A).
// Color-blind safe: shape (dot vs dash) + title tooltip for screen readers.
const dotColor: Record<ECG['hl7_status'], string> = {
  pending: 'bg-gray-400',
  success: 'bg-green-600',
  hl7_exhausted: 'bg-amber-600',
}

export function HL7StatusIcon({ status }: Props) {
  const { t } = useTranslation()
  return (
    <span
      className={`inline-block h-2 w-2 rounded-full shrink-0 ${dotColor[status]}`}
      title={t(`ecg.status.${status}`)}
      aria-label={t(`ecg.status.${status}`)}
    />
  )
}
