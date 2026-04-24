import { useCallback, useState } from 'react'

const STORAGE_KEY = 'ecg.download.formats'
const DEFAULT: string[] = ['original']

function readStorage(): string[] {
  try {
    const raw = localStorage.getItem(STORAGE_KEY)
    if (!raw) return DEFAULT
    const parsed = JSON.parse(raw) as unknown
    if (!Array.isArray(parsed)) return DEFAULT
    const cleaned = parsed.filter((v): v is string => typeof v === 'string' && v.length > 0)
    return cleaned.length > 0 ? cleaned : DEFAULT
  } catch {
    return DEFAULT
  }
}

function writeStorage(formats: string[]): void {
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(formats))
  } catch {
    // localStorage quota/privacy mode — fall back to in-memory state silently.
  }
}

// useDownloadFormatPrefs persists the user's last chosen download formats in
// localStorage so the popup re-opens with the same selection across ECGs and sessions.
// availableIds constrains the returned selection to formats that actually exist for
// the current context (unknown persisted ids are filtered out on read).
export function useDownloadFormatPrefs(availableIds: string[] | undefined) {
  const [stored, setStored] = useState<string[]>(() => readStorage())

  const save = useCallback((next: string[]) => {
    const deduped = Array.from(new Set(next.filter((v) => v.length > 0)))
    writeStorage(deduped)
    setStored(deduped)
  }, [])

  // Filter down to what's actually offered; fall back to 'original' when nothing matches.
  const selected = (() => {
    if (!availableIds || availableIds.length === 0) return stored
    const set = new Set(availableIds)
    const filtered = stored.filter((id) => set.has(id))
    if (filtered.length > 0) return filtered
    return availableIds.includes('original') ? ['original'] : [availableIds[0]]
  })()

  return { selected, save }
}
