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
  date_added?: string
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
  supports?: Support[] | null
  type?: string
  quote: string
  source: string
  date: string
  weight: string
}

export interface Support {
  text?: string
  catalog_id?: number
  title?: string
  insight?: string
}

export interface ChainLink {
  cluster: string
  evidence: string
}

export interface AnalyticsConfig {
  pull_quote?: string
  pull_quote_meta?: string
  inference_chain?: ChainLink[] | null
  patterns: Pattern[] | null
  gaps: Gap[] | null
  contradictions: Contradiction[] | null
  manifesto_quotes: ManifestoQuote[] | null
}

export interface GraphNode {
  category: string
  count: number
}

export interface GraphEdge {
  from: string
  to: string
  weight: number
}

export interface Graph {
  nodes: GraphNode[]
  edges: GraphEdge[]
}

export interface ChangelogRelease {
  version: string
  date: string | null
  tagline: string
  sections: Record<string, string[]>
}

export interface DocCard {
  title: string
  body?: string
  meta?: string
  badge?: string
  url?: string
  tags?: string[]
}

export interface DocSection {
  title: string
  note?: string
  cards?: DocCard[]
}

/** The generic shape of an owner-supplied view (team.json / projects.json). */
export interface Document {
  label?: string
  title?: string
  subtitle?: string
  sections?: DocSection[]
}

export interface Now {
  markdown: string
}

export interface Changelog {
  current_version: string
  current_date: string | null
  current_tagline: string
  releases: ChangelogRelease[] | null
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

/** Одна строка разреза с одним ключом: категория, место, источник. */
export interface NamedTotal {
  name: string
  total: string
  count: number
}

/** Подкатегория внутри категории. Склейка «Категория → Подкатегория» — дело вида. */
export interface SubcategoryTotal {
  category: string
  subcategory: string
  total: string
  count: number
}

/** Календарный период: YYYY-MM для месяцев, YYYY-MM-DD для дней. */
export interface PeriodTotal {
  period: string
  total: string
  count: number
}

/**
 * Арифметика финансов, посчитанная сервером. Считает он, а не фронт: иначе
 * реализаций было бы две и они обязаны были бы совпадать.
 *
 * Периоды приходят только те, где расходы ЕСТЬ. Заполнять пропуски — дело
 * графика, потому что окно (плотность за 31 день) знает он.
 */
export interface FinanceSummary {
  expenseCount: number
  expenses: string
  incomeCount: number
  income: string
  net: string
  byCategory: NamedTotal[]
  byAccount: NamedTotal[]
  byPlace: NamedTotal[]
  bySource: NamedTotal[]
  incomeBySource: NamedTotal[]
  bySubcategory: SubcategoryTotal[]
  byMonth: PeriodTotal[]
  byDay: PeriodTotal[]
}

async function getJSON<T>(path: string): Promise<T> {
  const res = await fetch(path)
  if (!res.ok) {
    throw new Error(`${path}: ${res.status} ${res.statusText}`)
  }
  return res.json() as Promise<T>
}

/** Всё, что нужно дашборду на первом экране, одним запросом-пачкой. */
export interface Dashboard {
  stats: Stats
  entries: Entry[]
  audits: Audits
  duplicates: DuplicateGroup[]
  analytics: Analytics
  analyticsConfig: AnalyticsConfig
  finances: Finances
}

export const api = {
  stats: () => getJSON<Stats>('/api/stats'),
  entries: () => getJSON<Entry[]>('/api/entries'),
  audits: () => getJSON<Audits>('/api/audits'),
  duplicates: () => getJSON<DuplicateGroup[]>('/api/duplicates'),
  analytics: () => getJSON<Analytics>('/api/analytics'),
  analyticsConfig: () => getJSON<AnalyticsConfig>('/api/analytics-config'),
  graph: () => getJSON<Graph>('/api/graph'),
  changelog: () => getJSON<Changelog>('/api/changelog'),
  now: () => getJSON<Now | null>('/api/now'),
  team: () => getJSON<Document | null>('/api/team'),
  projects: () => getJSON<Document | null>('/api/projects'),
  finances: () => getJSON<Finances>('/api/finances'),

  /**
   * Сводка за выбранные месяцы (YYYY-MM). Пустой список — за всё время.
   *
   * Период уходит на сервер, а не применяется к готовой полной сводке здесь:
   * иначе рядом с серверной арифметикой появилась бы вторая, клиентская,
   * обязанная с ней совпадать.
   */
  financeSummary: (months: string[] = []) =>
    getJSON<FinanceSummary>(
      months.length > 0
        ? `/api/finances/summary?months=${encodeURIComponent(months.join(','))}`
        : '/api/finances/summary',
    ),

  async dashboard(): Promise<Dashboard> {
    const [stats, entries, audits, duplicates, analytics, analyticsConfig, finances] =
      await Promise.all([
        api.stats(),
        api.entries(),
        api.audits(),
        api.duplicates(),
        api.analytics(),
        api.analyticsConfig(),
        // Финансы читают два файла, которые правят руками при открытом
        // дашборде, — этот запрос может упасть сам по себе, например пока
        // LibreOffice сохраняет. Остальные шесть видов из-за него падать не
        // должны: вид финансов уже умеет рендерить пустоту.
        api.finances().catch(() => ({ transactions: [], accounts: [] })),
      ])
    return { stats, entries, audits, duplicates, analytics, analyticsConfig, finances }
  },
}
