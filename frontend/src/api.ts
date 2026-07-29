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

export interface Pattern {
  name: string
  clusters: string[] | null
  desc: string
}

export interface Gap {
  topic: string
  clusters: string[] | null
  priority: string
}

export interface Contradiction {
  title: string
  a: string
  b: string
  resolution: string
}

export interface ManifestoQuote {
  quote: string
  source: string
  date: string
  weight: string
}

export interface AnalyticsConfig {
  patterns: Pattern[] | null
  gaps: Gap[] | null
  contradictions: Contradiction[] | null
  manifesto_quotes: ManifestoQuote[] | null
}

// Amounts arrive as decimal strings, not numbers: the ledger stores kopecks as
// int64 so that 89.99 stays 89.99, and parsing straight into a float would put
// 89.98999999999999 back on the screen. Anything that sums amounts goes through
// kopecks (see toKopecks in views).
export interface Transaction {
  id: string
  kind: 'expense' | 'income'
  date: string
  amount: string
  category?: string
  subcategory?: string
  place?: string
  description?: string
  account?: string
  source?: string
}

export interface Account {
  bank: string
  balance: string
  updated: string
}

export interface Finances {
  transactions: Transaction[]
  accounts: Account[]
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
  analyticsConfig: () => getJSON<AnalyticsConfig>('/api/analytics-config'),
  finances: () => getJSON<Finances>('/api/finances'),
}
