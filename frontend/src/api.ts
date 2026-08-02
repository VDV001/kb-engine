// Typed client for the kbengine HTTP API.

export interface Stats {
  total: number
  by_category: Record<string, number>
  by_lifecycle: Record<string, number>
  by_verdict: Record<string, number>
  by_kind: Record<string, number>
  /** Как каталог называет свои категории: ключ → «Название: описание». */
  category_labels?: Record<string, string>
  /** Читаемые названия тегов. Есть только у тех ключей, что заменили русские
   * теги: остальные читаемы сами по себе и в словаре отсутствуют. */
  tag_labels?: Record<string, string>
  health: Health
}

/** Насколько база разобрана: доли и их среднее, посчитанные на сервере. */
export interface Health {
  total: number
  processed: number
  with_notes: number
  /** Знаменатель доли конспектов: разобранные статьи, без своих материалов. */
  notes_base: number
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
  /** Когда материал вышел у автора — не то же, что дата попадания в базу. */
  habr_date?: string
  /** Когда его прочли здесь целиком, а не пролистали. */
  deep_read_date?: string
  date_created?: string
  description?: string
  author?: string
  source?: string
  /** Перевод чужого оригинала. Раньше жило словом «[Перевод]» в заголовке. */
  is_translation?: boolean
  /** Путь к собственному тексту записи: разбор, стандарт, публикация. Есть у
   * той записи, которой файл принадлежит, — после ADR-0004 ровно у одной. */
  file?: string
  /** Связанные записи. Для статьи здесь лежит её разбор. */
  related_ids?: number[]
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

/**
 * Что последний скан узнал про адреса базы.
 *
 * `alive` считается вычитанием на сервере: код ответа записывается только для
 * не-200, поэтому у живой ссылки есть дата проверки и нет кода. `undecidable` —
 * отдельное состояние намеренно: habr отвечает 403 и на снятую статью, и на
 * бота, и приписать такие к живым или к мёртвым значило бы соврать.
 */
export interface LinkHealth {
  alive: number
  moved: number
  gone: number
  undecidable: number
  unchecked: number
  with_url: number
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
  /** Опоры под выводом: без них счётчик «9 опор» — обещание без покрытия. */
  pull_quote_supports?: QuoteSupport[] | null
  contradiction_resolution?: string
  /** Что AI делает с категорией. Ядро первого манифестного тезиса. */
  amplify_clusters?: string[] | null
  replace_clusters?: string[] | null
  neutral_clusters?: string[] | null
}

export interface QuoteSupport {
  cluster: string
  claim: string
}

export interface GraphNode {
  category: string
  count: number
}

export interface GraphEdge {
  from: string
  to: string
  weight: number
  /** Подписи владельца: у одной связи бывает несколько смыслов сразу. */
  labels?: string[]
}

/** Подпись, которой не нашлось ребра в вычисленном графе. */
export interface CuratedLink {
  from: string
  to: string
  label: string
}

export interface Graph {
  nodes: GraphNode[]
  edges: GraphEdge[]
  /** Сколько связей подписано вручную и сколько нет — без этих чисел
   * несколько выделенных связей читаются как размеченный целиком граф. */
  labeled?: number
  unlabeled?: number
  /** Подписей всего: больше, чем labeled, когда связь несёт несколько смыслов. */
  label_count?: number
  unplaced_links?: CuratedLink[]
}

export interface ChangelogRelease {
  version: string
  date: string | null
  tagline: string
  sections: Record<string, string[]>
}

export interface DocCard {
  title: string
  /** Подпись над заголовком: чья это роль, а не кто её занимает. */
  eyebrow?: string
  body?: string
  /** Зона ответственности по пунктам. Абзацем её не читают — ищут свою строку. */
  points?: string[]
  meta?: string
  badge?: string
  url?: string
  tags?: string[]
  /**
   * Участники, между которыми идёт этот шаг. Заданы явно, а не разобраны из
   * заголовка: в тексте одно и то же лицо пишется то «Отдел», то «отдел», и
   * парсер завёл бы два узла вместо одного. Карточка без from/to — обычное
   * описание, в схему она не попадает.
   */
  from?: string
  to?: string
  /** Промежуточный участник: «Заказчик → Данил → Даниил» это два ребра. */
  via?: string
  /** Куда идёт шаг: задача вниз (по умолчанию) или статус наверх. */
  kind?: 'task' | 'status'
}

export interface DocSection {
  title: string
  note?: string
  /**
   * В секции есть персональные данные — под маской заголовки её карточек
   * скрываются. Признак идёт из файла, а не выводится рендером: в одном
   * разделе заголовок карточки — имя человека, в другом — шаг процесса
   * («Продажник → отдел»), и спрятать второе значит стереть саму схему.
   */
  sensitive?: boolean
  cards?: DocCard[]
}

/** The generic shape of an owner-supplied view (team.json). */
export interface Document {
  label?: string
  title?: string
  subtitle?: string
  sections?: DocSection[]
}

/** Одна цифра с подписью: и в шапке страницы, и в полосе метрик карточки. */
export interface Metric {
  value: string
  label: string
}

export interface DocLink {
  label: string
  url: string
}

/**
 * Ширина карточки в сетке. Лежит в данных, а не выводится из их полноты:
 * правило вида «карточка с кодом — широкая» тихо переставляет вёрстку в тот
 * день, когда у проекта появляется ещё одна метрика.
 */
export type CardSpan = 'full' | 'half' | 'third'

/**
 * Карточка проекта. Шире DocCard, потому что страницу показывают заказчику:
 * plain объясняет продукт через боль без терминов, image даёт скриншот вместо
 * абстрактного градиента, metrics и links отвечают на «а это работает?».
 */
export interface ProjectCard extends DocCard {
  short?: string
  kicker?: string
  note?: string
  plain?: string
  image?: string
  span?: CardSpan
  metrics?: Metric[]
  links?: DocLink[]
  /** Имя градиента из палитры или готовое значение CSS. */
  accent?: string
  code?: string[]
}

export interface ProjectSection {
  title: string
  note?: string
  cards?: ProjectCard[]
}

export interface TechGroup {
  title: string
  items: { name: string; value?: string }[]
}

export interface Contact {
  /** Имя иконки из ICONS; неизвестное имя рисуется без иконки. */
  icon?: string
  label: string
  value?: string
  url: string
}

/** The owner-supplied Projects view: a portfolio he shows to clients. */
export interface ProjectDoc {
  label?: string
  title?: string
  subtitle?: string
  stats?: Metric[]
  sections?: ProjectSection[]
  tech?: TechGroup[]
  contacts?: Contact[]
  footer?: { name?: string; tagline?: string; place?: string }
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

/** Сборка движка, которая прямо сейчас отвечает на запросы. Версия базы и
 * версия движка — разные вещи: первая про содержимое, вторая про программу. */
export interface Engine {
  version: string
  commit: string
  built: string
  /**
   * Какие необязательные источники движку передали при запуске.
   *
   * Нужно затем, чтобы вкладка могла отличить «в базе ничего нет» от «файл не
   * попросили загрузить»: о флагах командной строки она сама не знает, а
   * пустой список этих двух случаев не различает. Старая сборка поля не
   * отдаёт вовсе — отсюда `?`, и промолчать тогда честнее, чем выдумать.
   */
  sources?: SourceStatus[] | null
}

/** Один необязательный источник: имя флага, как в команде, и факт подключения. */
export interface SourceStatus {
  flag: string
  connected: boolean
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

/** Одна строка журнала так, как её увозят в книгу. */
export interface FinanceExportRow {
  date: string
  category: string
  subcategory: string
  place: string
  description: string
  amount: string
  account: string
}

export const api = {
  stats: () => getJSON<Stats>('/api/stats'),
  entries: () => getJSON<Entry[]>('/api/entries'),
  audits: () => getJSON<Audits>('/api/audits'),
  // Пустой список приходил как null: тип обещал массив, а сервер писал nil-слайс.
  // Сервер это чинит у себя, но нормализация остаётся и здесь — клиент не должен
  // белеть от того, чем ему ответили, а сервером может оказаться и старая сборка.
  duplicates: () =>
    getJSON<DuplicateGroup[] | null>('/api/duplicates').then((groups) => groups ?? []),
  linkHealth: () => getJSON<LinkHealth>('/api/link-health'),
  analytics: () => getJSON<Analytics>('/api/analytics'),
  analyticsConfig: () => getJSON<AnalyticsConfig>('/api/analytics-config'),
  graph: () => getJSON<Graph>('/api/graph'),
  changelog: () => getJSON<Changelog>('/api/changelog'),
  engine: () => getJSON<Engine>('/api/engine'),
  now: () => getJSON<Now | null>('/api/now'),
  team: () => getJSON<Document | null>('/api/team'),
  projects: () => getJSON<ProjectDoc | null>('/api/projects'),
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

  /**
   * Экспорт журнала книгой xlsx. Отправляем строки, а не параметры фильтра:
   * фильтрует и сортирует вид, и повторять эти правила на сервере значило бы
   * держать две реализации одного, обязанные совпадать.
   */
  async financeExport(rows: FinanceExportRow[]): Promise<Blob> {
    const res = await fetch('/api/finances/export', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ rows }),
    })
    if (!res.ok) throw new Error(`export failed: ${res.status}`)
    return res.blob()
  },

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
