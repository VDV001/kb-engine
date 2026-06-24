// Typed client for the kbengine HTTP API.

export interface Stats {
  total: number
  by_category: Record<string, number>
  by_lifecycle: Record<string, number>
  by_verdict: Record<string, number>
  by_kind: Record<string, number>
}

export interface Entry {
  id: number
  habr_id?: number
  title: string
  url?: string
  category: string
  kind: string
  lifecycle: string
  verdict?: string
  read_state?: string
  publish_stage?: string
  tags?: string[]
  description?: string
  author?: string
  source?: string
}

export interface Finding {
  EntryID: number
  Title: string
  Current: string
  Reasons: string[]
}

export interface Audits {
  outdated: Finding[] | null
  canonical: Finding[] | null
  supersession: Finding[] | null
}

export interface DuplicateGroup {
  Kind: string
  Key: string
  EntryIDs: number[]
}

export interface WeekCount {
  week: string
  count: number
}

export interface CategorySize {
  category: string
  count: number
}

export interface Analytics {
  growth: WeekCount[]
  categories: CategorySize[]
}

async function getJSON<T>(path: string): Promise<T> {
  const res = await fetch(path)
  if (!res.ok) {
    throw new Error(`${path}: ${res.status} ${res.statusText}`)
  }
  return res.json() as Promise<T>
}

export const api = {
  stats: () => getJSON<Stats>('/api/stats'),
  entries: () => getJSON<Entry[]>('/api/entries'),
  audits: () => getJSON<Audits>('/api/audits'),
  duplicates: () => getJSON<DuplicateGroup[]>('/api/duplicates'),
  analytics: () => getJSON<Analytics>('/api/analytics'),
}
