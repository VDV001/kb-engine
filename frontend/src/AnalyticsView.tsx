import { useState } from 'react'
import { api } from './api'
import type { AnalyticsConfig, Contradiction, Gap, Graph, ManifestoQuote, Pattern, Stats, Support } from './api'
import { useResource } from './hooks/useResource'
import { categoryLabel } from './catalog'
import { GraphConclusions } from './components/KnowledgeGraph'
import { Icon } from './components/Icon'
import type { IconName } from './components/icons'
import { Label } from './components/ui'

// The meta-analytics view, ported from the KB dashboard: five tabs over the
// curated semantic layer, a dark summary card on the right. Everything comes
// from the API on mount — the config is re-read server-side per request and
// the graph is computed from the catalog, so new entries reshape both without
// a rebuild.

type TabId = 'манифест' | 'паттерны' | 'противоречия' | 'пробелы' | 'граф'
const tabIds: TabId[] = ['манифест', 'паттерны', 'противоречия', 'пробелы', 'граф']

function supportLine(s: Support): string {
  if (s.text) return s.text
  const ref = [s.catalog_id ? `catalog#${s.catalog_id}` : '', s.title].filter(Boolean).join(', ')
  return s.insight ? `${ref}: ${s.insight}` : ref
}

/**
 * Типы манифестных тезисов: каждый отвечает на свой вопрос, и цвет полосы —
 * единственное, что отличает карточки друг от друга с расстояния.
 */
const WEIGHTS: Record<string, { label: string; tone: string; bar: string; icon: IconName }> = {
  primary: { label: 'Что делает AI', tone: 'text-primary', bar: 'var(--primary)', icon: 'psychology' },
  mechanism: {
    label: 'Как именно',
    tone: 'text-secondary',
    bar: 'var(--secondary)',
    icon: 'precision_manufacturing',
  },
  'founding-architecture': {
    label: 'Где живёт результат',
    tone: 'text-on-surface',
    bar: 'var(--on-surface)',
    icon: 'hub',
  },
  reliability: {
    label: 'Как именно думает',
    tone: 'text-on-surface',
    bar: 'var(--on-surface-variant)',
    icon: 'verified_user',
  },
  understanding: {
    label: 'Что должно остаться у человека',
    tone: 'text-secondary',
    bar: 'var(--secondary)',
    icon: 'school',
  },
}

function QuoteCard({ q, index, first }: { q: ManifestoQuote; index: number; first: boolean }) {
  const [open, setOpen] = useState(first)
  const supports = q.supports ?? []
  const w = WEIGHTS[q.weight] ?? {
    label: q.type ?? q.weight,
    tone: 'text-on-surface-variant',
    bar: 'var(--outline-variant)',
    icon: 'menu_book',
  }

  return (
    <article
      className="rounded-xl border border-outline-variant bg-surface-lowest p-8"
      style={{ borderLeft: `4px solid ${w.bar}` }}
    >
      <div className="mb-4 flex items-start justify-between gap-4">
        <div className="flex items-center gap-2.5">
          <Icon name={w.icon} className={`size-5 ${w.tone}`} />
          <span className={`label ${w.tone}`}>{w.label}</span>
        </div>
        <span className="font-headline text-sm font-semibold italic text-on-surface-variant opacity-60">
          №{index + 1}
        </span>
      </div>

      <blockquote className="font-headline text-lg italic leading-relaxed">«{q.quote}»</blockquote>

      <p className="mt-4 border-t border-outline-variant pt-3 text-xs text-on-surface-variant">
        {q.source}
        {q.date && <span className="ml-2 opacity-60">· {q.date}</span>}
      </p>

      {supports.length > 0 && (
        <div className="mt-3">
          <button
            type="button"
            onClick={() => setOpen(!open)}
            className="label hover:text-on-surface"
            aria-expanded={open}
          >
            {open ? '− опоры' : `+ опоры (${supports.length})`}
          </button>
          {open && (
            <ul className="mt-2 list-disc space-y-1.5 pl-5 text-sm leading-relaxed text-on-surface-variant">
              {supports.map((s, i) => (
                <li key={i}>{supportLine(s)}</li>
              ))}
            </ul>
          )}
        </div>
      )}
    </article>
  )
}

const FULL_REACH = 12

/**
 * PatternCard — карточка паттерна: бейдж охвата, текст и полоса внизу.
 * Свёрнута по умолчанию: у описаний тут по десять строк, и развёрнутыми они
 * превращают вкладку в стену. Шапка и охват видны всегда — по ним и выбирают,
 * что раскрыть.
 */
function PatternCard({ pattern, rank }: { pattern: Pattern; rank: number }) {
  const [open, setOpen] = useState(rank === 0)
  const clusters = pattern.clusters ?? []
  const reach = clusters.length
  const pct = Math.min(100, Math.round((reach / FULL_REACH) * 100))
  const tone = reach >= 5 ? 'text-primary' : reach >= 4 ? 'text-secondary' : 'text-on-surface-variant'

  return (
    <article className="flex flex-col rounded-xl border border-outline-variant bg-surface-lowest p-7">
      <div className="mb-5 flex items-start justify-between gap-3">
        <span className="label rounded-sm bg-surface-high px-2.5 py-1">
          {reach} {reach === 1 ? 'кластер' : reach < 5 ? 'кластера' : 'кластеров'}
        </span>
        <Icon name="psychology" className={`size-5 opacity-70 ${tone}`} />
      </div>

      <h3 className="font-headline text-xl font-bold leading-snug">{pattern.name}</h3>

      {open && (
        <>
          <p className="mt-2.5 text-sm leading-relaxed text-on-surface-variant">{pattern.desc}</p>
          {clusters.length > 0 && (
            <div className="mt-4 flex flex-wrap gap-1">
              {clusters.map((c) => (
                <span
                  key={c}
                  className="rounded-sm border border-outline-variant bg-surface-high px-1.5 py-0.5 text-[10px] text-on-surface-variant"
                >
                  {c}
                </span>
              ))}
            </div>
          )}
        </>
      )}

      <button
        type="button"
        onClick={() => setOpen(!open)}
        className="mt-4 self-start text-xs text-on-surface-variant hover:text-on-surface"
        aria-expanded={open}
      >
        {open ? '− свернуть' : '+ развернуть'}
      </button>

      <div className="mt-auto pt-5">
        <div className="mb-1.5 flex items-end justify-between">
          <span className="label text-secondary">Охват</span>
          <span className={`font-headline text-sm italic ${tone}`}>
            {reach}/{FULL_REACH}
          </span>
        </div>
        <div className="h-0.5 w-full bg-outline-variant">
          <div className={`h-0.5 ${reach >= 5 ? 'bg-primary' : 'bg-secondary'}`} style={{ width: `${pct}%` }} />
        </div>
      </div>
    </article>
  )
}

/** ContradictionCard — два тезиса рядом и развязка под ними. Тезисы стоят
 * колонками именно потому, что противоречие читается сравнением, а не списком. */
function ContradictionCard({ c, index }: { c: Contradiction; index: number }) {
  return (
    <article className="rounded-xl border border-outline-variant bg-surface-lowest p-7">
      <div className="mb-5 flex items-center gap-2.5">
        <Icon name="balance" className="size-4 text-secondary" />
        <span className="label text-secondary">Противоречие {index + 1}</span>
      </div>

      <h3 className="mb-4 font-headline text-xl font-bold">{c.title}</h3>

      <div className="mb-3 grid gap-3 sm:grid-cols-2">
        <div className="rounded border border-primary/20 bg-primary/[0.07] p-3.5">
          <span className="label mb-1.5 block text-primary">Тезис A</span>
          <span className="text-sm leading-relaxed">{c.a}</span>
        </div>
        <div className="rounded border border-outline-variant bg-surface-high/60 p-3.5">
          <span className="label mb-1.5 block text-secondary">Тезис B</span>
          <span className="text-sm leading-relaxed">{c.b}</span>
        </div>
      </div>

      {c.resolution && (
        <div className="rounded border border-outline-variant bg-surface-high p-3.5">
          <span className="label mb-1.5 block">Разрешение</span>
          <span className="text-sm leading-relaxed">{c.resolution}</span>
        </div>
      )}
    </article>
  )
}


/** Приоритет пробела: подпись, иконка и цвет полосы. Иконки взяты из набора
 * движка — «!» и «внимание» в нём нет, роль играют смысловые ближайшие. */
const GAP_PRIORITY: Record<string, { label: string; icon: IconName; bar: string }> = {
  high: { label: 'Высокий', icon: 'trending_up', bar: 'var(--primary)' },
  medium: { label: 'Средний', icon: 'update', bar: 'var(--secondary)' },
  low: { label: 'Низкий', icon: 'history', bar: 'var(--outline-variant)' },
}

/** Сколько кластеров считается полным охватом пробела — знаменатель исходника. */
const GAP_FULL = 5

/** GapCard — карточка пробела: приоритет, тема, кластеры и полоса охвата.
 * Здесь сворачивать нечего: у пробела нет длинного описания, только тема и метки. */
function GapCard({ gap }: { gap: Gap }) {
  const clusters = gap.clusters ?? []
  const m = GAP_PRIORITY[gap.priority] ?? GAP_PRIORITY.low
  const pct = Math.min(100, Math.round((clusters.length / GAP_FULL) * 100))

  return (
    <article className="flex min-h-[13rem] flex-col justify-between rounded-xl border border-outline-variant bg-surface-lowest p-7">
      <div>
        <div className="mb-4 flex items-start justify-between gap-3">
          <span className="label rounded-sm bg-surface-high px-2.5 py-1">{m.label}</span>
          <Icon name={m.icon} className="size-5 text-outline-variant" />
        </div>
        <h3 className="mb-3 font-headline text-base font-bold leading-snug">{gap.topic}</h3>
        {clusters.length > 0 && (
          <div className="flex flex-wrap gap-1">
            {clusters.map((c) => (
              <span
                key={c}
                className="rounded-sm border border-outline-variant bg-surface-high px-1.5 py-0.5 text-[10px] text-on-surface-variant"
              >
                {c}
              </span>
            ))}
          </div>
        )}
      </div>

      <div className="mt-5">
        <div className="mb-1.5 flex items-end justify-between">
          <span className="label text-secondary">Кластеров</span>
          <span className="font-headline text-sm italic text-primary">{clusters.length}</span>
        </div>
        <div className="h-0.5 w-full bg-outline-variant">
          <div className="h-0.5" style={{ width: `${pct}%`, background: m.bar }} />
        </div>
      </div>
    </article>
  )
}


export function AnalyticsView({ config, stats }: { config: AnalyticsConfig; stats: Stats }) {
  const [tab, setTab] = useState<TabId>('манифест')
  const [chainOpen, setChainOpen] = useState(false)
  const [supportsOpen, setSupportsOpen] = useState(false)
  // Граф грузится только когда на вкладку зашли, и один раз за жизнь вида.
  // Падение запроса рендерится как пустой граф: подпись рядом обещает связи из
  // каталога, и пустая картинка честнее ошибки на весь экран.
  const res = useResource(api.graph, { enabled: tab === 'граф' })
  const graph: Graph | null =
    res.status === 'ready' ? res.data : res.status === 'failed' ? { nodes: [], edges: [] } : null

  const patterns = config.patterns ?? []
  const contradictions = config.contradictions ?? []
  const gaps = config.gaps ?? []
  const quotes = config.manifesto_quotes ?? []
  const chain = config.inference_chain ?? []
  // Правый столбец: опоры под выводом и разбиение категорий по тому, что с
  // ними делает AI. Всё это лежало в конфиге и обрезалось по дороге.
  const supports = config.pull_quote_supports ?? []
  const amplify = config.amplify_clusters ?? []
  const replace = config.replace_clusters ?? []
  const neutral = config.neutral_clusters ?? []
  const clusters = Object.keys(stats.by_category).length
  const topClusters = Object.entries(stats.by_category)
    .sort((a, b) => b[1] - a[1])
    .slice(0, 5)
  // Дата берётся при отрисовке: панель обещает срез на сегодня.
  const today = new Date().toLocaleDateString('ru-RU')

  return (
    <div className="space-y-6">
      <header>
        <Label className="text-secondary">
          KB · Level 2 analysis · {stats.total} записи
        </Label>
        <h1 className="mt-1 text-4xl">Мета-аналитика.</h1>
        <p className="mt-2 text-sm text-on-surface-variant">
          Паттерны, противоречия и пробелы поверх {clusters} кластеров.
        </p>
      </header>

      <div className="flex flex-col gap-8 xl:flex-row">
        <div className="min-w-0 flex-1 space-y-5">
          <nav className="flex gap-6 overflow-x-auto border-b border-outline-variant">
            {tabIds.map((t) => (
              <button
                key={t}
                type="button"
                onClick={() => setTab(t)}
                className={`label whitespace-nowrap border-b-2 pb-2 ${
                  tab === t
                    ? 'border-secondary text-on-surface'
                    : 'border-transparent hover:text-on-surface'
                }`}
              >
                {t}
              </button>
            ))}
          </nav>

          {tab === 'манифест' && (
            <div className="space-y-6">
              <div>
                <h2 className="font-headline text-2xl font-bold">Манифест</h2>
                <p className="mt-1 text-sm text-on-surface-variant">
                  Фундаментальные тезисы, на которых строится KB
                </p>
              </div>
              {quotes.map((q, i) => (
                <QuoteCard key={i} q={q} index={i} first={i === 0} />
              ))}
            </div>
          )}

          {tab === 'паттерны' && (
            <div className="grid gap-5 xl:grid-cols-2">
              {patterns.map((p, i) => (
                <PatternCard key={p.name} pattern={p} rank={i} />
              ))}
            </div>
          )}

          {tab === 'противоречия' && (
            <div className="space-y-5">
              {contradictions.map((c, i) => (
                <ContradictionCard key={c.title} c={c} index={i} />
              ))}
            </div>
          )}

          {tab === 'пробелы' && (
            <div className="grid gap-5 xl:grid-cols-2">
              {[...gaps]
                .sort((a, b) => (b.clusters?.length ?? 0) - (a.clusters?.length ?? 0))
                .map((g) => (
                  <GapCard key={g.topic} gap={g} />
                ))}
            </div>
          )}

          {tab === 'граф' && (
            <div className="space-y-3">
              <p className="text-sm text-on-surface-variant">
                Сам граф живёт на дашборде — рисовать одну и ту же топологию на двух экранах значит
                держать две картинки, которые однажды разойдутся. Здесь остаются выводы из неё.
              </p>
              {graph === null ? (
                <p className="p-8 text-center text-on-surface-variant">Загрузка…</p>
              ) : (
                <GraphConclusions graph={graph} labels={stats.category_labels ?? {}} total={stats.total} />
              )}
            </div>
          )}
        </div>

        <aside className="shrink-0 xl:w-[340px]">
          <div className="sticky top-24 overflow-hidden rounded-xl border border-outline-variant bg-surface-low shadow-lg">
            {/* Инвертированная полоса — газетный колонтитул: она отделяет
                сводку от страницы сильнее любой рамки. */}
            <div className="flex items-center justify-between bg-on-surface px-6 py-3.5">
              <span className="font-label text-[10px] font-bold uppercase tracking-[0.28em] text-bg">
                KB / Аналитика
              </span>
              <span className="font-label text-[10px] text-bg opacity-40">{today}</span>
            </div>

            <div className="border-b border-outline-variant px-6 pb-5 pt-6">
              <div className="flex items-baseline gap-2">
                <span className="font-headline text-[3.5rem] font-bold leading-none tabular-nums">
                  {stats.total}
                </span>
                <span className="text-xs text-on-surface-variant">записи</span>
              </div>
              <p className="mt-1 text-xs text-on-surface-variant">
                {clusters} кластеров · {chain.length} концептуальных связей
              </p>
            </div>

            <div className="grid grid-cols-2 border-b border-outline-variant">
              {[
                [clusters, 'кластеров', 'text-on-surface'],
                [patterns.length, 'паттернов', 'text-primary'],
                [contradictions.length, 'противоречий', 'text-secondary'],
                [gaps.length, 'пробелов', 'text-on-surface-variant'],
              ].map(([n, label, tone], i) => (
                <div
                  key={String(label)}
                  className={`px-5 py-4 ${i % 2 === 0 ? 'border-r border-outline-variant' : ''} ${i < 2 ? 'border-b border-outline-variant' : ''}`}
                >
                  <div className={`font-headline text-2xl font-bold leading-none tabular-nums ${tone}`}>{n}</div>
                  <div className="label mt-1 opacity-70">{label}</div>
                </div>
              ))}
            </div>

            {config.pull_quote && (
              <div className="border-b border-l-[3px] border-outline-variant border-l-primary px-6 py-5">
                <blockquote className="font-headline text-sm italic leading-relaxed">
                  «{config.pull_quote}»
                </blockquote>
                {config.pull_quote_meta && <p className="label mt-2">{config.pull_quote_meta}</p>}

                {chain.length > 0 && (
                  <div className="mt-4 border-t border-outline-variant pt-4">
                    <button
                      type="button"
                      onClick={() => setChainOpen(!chainOpen)}
                      className="flex w-full items-center justify-between gap-2"
                      aria-expanded={chainOpen}
                    >
                      <span className="label">Откуда это следует</span>
                      <span className="label opacity-60">
                        {chainOpen ? 'скрыть' : `${chain.length} опор`}
                      </span>
                    </button>
                    {chainOpen && (
                      <dl className="mt-3 space-y-2.5">
                        {chain.map((l) => (
                          <div key={l.cluster} className="grid grid-cols-[7rem_1fr] gap-2">
                            <dt className="text-[11px] font-bold">{l.cluster}</dt>
                            <dd className="text-[11px] leading-relaxed text-on-surface-variant">
                              {l.evidence}
                            </dd>
                          </div>
                        ))}
                      </dl>
                    )}
                  </div>
                )}
              </div>
            )}

            {config.contradiction_resolution && (
              <div className="border-b border-outline-variant bg-surface-high px-6 py-5">
                <p className="label mb-2 text-secondary">Разрешение противоречия</p>
                <p className="text-[11px] leading-relaxed text-on-surface-variant">
                  {config.contradiction_resolution}
                </p>
              </div>
            )}

            {supports.length > 0 && (
              <div className="border-b border-outline-variant px-6 py-5">
                {/* Опор почти семьдесят: развёрнутые они длиннее самой страницы,
                    поэтому свёрнуты, но счётчик обещает ровно то, что раскроется. */}
                <button
                  type="button"
                  onClick={() => setSupportsOpen(!supportsOpen)}
                  className="flex w-full items-center justify-between gap-2"
                  aria-expanded={supportsOpen}
                >
                  <span className="label">Опоры вывода</span>
                  <span className="label opacity-60">
                    {supportsOpen ? 'скрыть' : String(supports.length)}
                  </span>
                </button>
                {supportsOpen && (
                  <dl className="mt-3 space-y-2.5">
                    {supports.map((sp, i) => (
                      <div key={`${sp.cluster}-${i}`} className="grid grid-cols-[7rem_1fr] gap-2">
                        <dt className="text-[11px] font-bold text-secondary">{sp.cluster}</dt>
                        <dd className="text-[11px] leading-relaxed text-on-surface-variant">
                          {sp.claim}
                        </dd>
                      </div>
                    ))}
                  </dl>
                )}
              </div>
            )}

            {(amplify.length > 0 || replace.length > 0 || neutral.length > 0) && (
              <div className="border-b border-outline-variant px-6 py-5">
                <p className="label mb-3">Направление кластеров</p>
                {/* Три группы, а не диаграмма: вопрос здесь «какие именно», и
                    имя категории отвечает на него, а доля — нет. */}
                {(
                  [
                    ['AI усиливает', amplify, 'text-on-surface'],
                    ['AI заменяет', replace, 'text-secondary'],
                    ['Нейтрально', neutral, 'text-on-surface-variant'],
                  ] as const
                ).map(([title, list, tone]) =>
                  list.length === 0 ? null : (
                    <div key={title} className="mb-3 last:mb-0">
                      <div className="flex items-baseline justify-between">
                        <span className={`label ${tone}`}>{title}</span>
                        <span className="font-headline text-xs font-bold italic tabular-nums">
                          {list.length}
                        </span>
                      </div>
                      <div className="mt-1.5 flex flex-wrap gap-1.5">
                        {list.map((c) => (
                          <span
                            key={c}
                            className="rounded border border-outline-variant bg-surface-high px-2 py-0.5 text-[10px]"
                            title={c}
                          >
                            {categoryLabel(c, stats.category_labels ?? {})}
                          </span>
                        ))}
                      </div>
                    </div>
                  ),
                )}
              </div>
            )}

            <div className="px-6 py-5">
              <p className="label mb-3">Топ-5 кластеров</p>
              <ul className="space-y-2">
                {topClusters.map(([key, n]) => (
                  <li key={key} className="flex items-baseline gap-2 text-xs">
                    <span className="min-w-0 flex-1 truncate" title={key}>
                      {categoryLabel(key, stats.category_labels ?? {})}
                    </span>
                    <span className="font-mono text-[10px] tabular-nums text-on-surface-variant">
                      {stats.total > 0 ? Math.round((n / stats.total) * 100) : 0}%
                    </span>
                  </li>
                ))}
              </ul>
            </div>
          </div>
        </aside>
      </div>
    </div>
  )
}
