import { useState } from 'react'
import { api } from './api'
import type { AnalyticsConfig, Graph, ManifestoQuote, Stats, Support } from './api'
import { useResource } from './hooks/useResource'
import { layoutGraph } from './graphLayout'
import { categoryLabel } from './catalog'
import { Card, Label } from './components/ui'

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

function QuoteCard({ q, first }: { q: ManifestoQuote; first: boolean }) {
  const [open, setOpen] = useState(first)
  const supports = q.supports ?? []
  return (
    <Card className="space-y-3">
      <div className="flex items-start justify-between gap-3">
        <Label className="text-secondary">{q.type ?? q.weight}</Label>
        <span className="label">{q.weight}</span>
      </div>
      <blockquote className="font-headline text-xl italic leading-relaxed">«{q.quote}»</blockquote>
      <p className="text-xs text-on-surface-variant">
        {q.source} · {q.date}
      </p>
      {supports.length > 0 && (
        <div className="border-t border-outline-variant pt-3">
          <button
            type="button"
            onClick={() => setOpen(!open)}
            className="label hover:text-on-surface"
          >
            {open ? '− опоры' : `+ опоры (${supports.length})`}
          </button>
          {open && (
            <ul className="mt-2 list-disc space-y-1.5 pl-5 text-sm text-on-surface-variant">
              {supports.map((s, i) => (
                <li key={i}>{supportLine(s)}</li>
              ))}
            </ul>
          )}
        </div>
      )}
    </Card>
  )
}

/**
 * KnowledgeGraph рисует топологию по пересечению тегов: самая крупная категория
 * в центре, остальные двумя эллиптическими кольцами, толщина связи — число
 * общих меток. Рядом нумерованный список: на холсте у узла тот же номер.
 *
 * Раскладка детерминирована и живёт в graphLayout — позиция считается из
 * порядкового номера, а не из симуляции, поэтому одна и та же база даёт одну и
 * ту же картинку, и снимок сравним со вчерашним.
 */
function KnowledgeGraph({ graph, labels, total }: { graph: Graph; labels: Record<string, string>; total: number }) {
  const W = 900
  const H = 580
  const l = layoutGraph(graph, { width: W, height: H })
  if (l.nodes.length === 0) return <p className="text-sm text-on-surface-variant">Нет данных.</p>

  return (
    <div className="overflow-x-auto border border-outline-variant bg-surface-lowest">
      <div className="flex min-w-[48rem]">
        <svg
          viewBox={`0 0 ${l.canvasWidth} ${H}`}
          style={{ width: `${(l.canvasWidth / W) * 100}%` }}
          role="img"
          aria-label="Граф знаний: топология категорий по общим тегам"
        >
          {/* Перекрестье и штампы — та же техническая рамка, что в исходном
              дашборде: они на пороге видимости и держат холст, а не украшают. */}
          <line x1={l.canvasWidth / 2} y1={0} x2={l.canvasWidth / 2} y2={H} stroke="currentColor" strokeWidth={0.5} opacity={0.04} />
          <line x1={0} y1={H / 2} x2={l.canvasWidth} y2={H / 2} stroke="currentColor" strokeWidth={0.5} opacity={0.04} />
          <rect x={14} y={14} width={96} height={18} fill="none" stroke="currentColor" strokeWidth={0.5} opacity={0.1} />
          <text x={18} y={26} className="font-label" fontSize={7} fill="currentColor" opacity={0.15} letterSpacing={1.5}>
            REF: KG-{String(l.nodes.length).padStart(2, '0')}-VX
          </text>
          <text x={l.canvasWidth - 12} y={H - 12} textAnchor="end" className="font-label" fontSize={6.5} fill="currentColor" opacity={0.1} letterSpacing={1}>
            VECTOR.MAP · {total} ENTRIES
          </text>

          {l.edges.map((e) => (
            <line
              key={`${e.from}-${e.to}`}
              x1={e.x1}
              y1={e.y1}
              x2={e.x2}
              y2={e.y2}
              stroke="var(--secondary)"
              strokeWidth={e.strokeWidth}
              opacity={e.opacity}
              strokeDasharray={e.strong ? undefined : '4 6'}
            />
          ))}

          {l.nodes.map((n) => (
            <g key={n.key}>
              <rect
                x={n.x - n.width / 2}
                y={n.y - n.height / 2}
                width={n.width}
                height={n.height}
                rx={4}
                fill={n.isHub ? 'var(--surface-high)' : 'var(--surface-lowest)'}
                stroke="var(--secondary)"
                strokeOpacity={n.isHub ? 0.9 : 0.45}
              />
              <text x={n.x - n.width / 2 + 6} y={n.y - n.height / 2 + 12} className="font-label" fontSize={7} fill="var(--on-surface-variant)" opacity={0.6}>
                {String(n.index).padStart(2, '0')}
              </text>
              <text x={n.x} y={n.y + 1} textAnchor="middle" fill="var(--on-surface)" style={{ font: `600 ${n.isHub ? 11 : 9}px var(--font-label)`, letterSpacing: '0.06em' }}>
                {truncate(categoryLabel(n.key, labels), n.isHub ? 16 : 12)}
              </text>
              <text x={n.x} y={n.y + (n.isHub ? 18 : 13)} textAnchor="middle" fill="var(--on-surface-variant)" style={{ font: '9px var(--font-mono)' }}>
                {n.count}
              </text>
            </g>
          ))}
        </svg>

        {/* Список узлов: на холсте подписи обрезаны по ширине прямоугольника,
            и полное имя категории видно только здесь. */}
        <aside className="shrink-0 border-l border-outline-variant p-4" style={{ width: `${(l.sideWidth / W) * 100}%` }}>
          <p className="label mb-3">Node index</p>
          <ol className="space-y-1.5">
            {l.nodes.map((n) => (
              <li key={n.key} className="flex items-baseline gap-2 text-xs">
                <span className="font-mono text-[10px] text-on-surface-variant tabular-nums">
                  {String(n.index).padStart(2, '0')}
                </span>
                <span className="min-w-0 flex-1 truncate text-on-surface" title={labels[n.key] || n.key}>
                  {categoryLabel(n.key, labels)}
                </span>
                <span className="font-mono text-[10px] text-on-surface-variant tabular-nums">{n.count}</span>
              </li>
            ))}
          </ol>
        </aside>
      </div>
    </div>
  )
}

/** Подпись обрезается по числу знаков, а не по ширине: ширина прямоугольника
 * известна заранее, и мерить текст в SVG значит считать то, что уже посчитано. */
function truncate(s: string, max: number): string {
  return s.length > max ? `${s.slice(0, max - 1)}…` : s
}

const priorityTone: Record<string, string> = {
  high: 'bg-tag-bg-4 text-tag-text-4',
  medium: 'bg-tag-bg-2 text-tag-text-2',
  low: 'bg-tag-bg-3 text-tag-text-3',
}

export function AnalyticsView({ config, stats }: { config: AnalyticsConfig; stats: Stats }) {
  const [tab, setTab] = useState<TabId>('манифест')
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
  const clusters = Object.keys(stats.by_category).length

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
            <div className="space-y-4">
              {quotes.map((q, i) => (
                <QuoteCard key={i} q={q} first={i === 0} />
              ))}
            </div>
          )}

          {tab === 'паттерны' && (
            <div className="grid gap-4 lg:grid-cols-2">
              {patterns.map((p) => (
                <Card key={p.name} className="space-y-2">
                  <h3 className="font-headline text-base font-bold">{p.name}</h3>
                  <p className="text-sm text-on-surface-variant">{p.desc}</p>
                </Card>
              ))}
            </div>
          )}

          {tab === 'противоречия' && (
            <div className="space-y-4">
              {contradictions.map((c) => (
                <Card key={c.title} className="space-y-3">
                  <h3 className="font-headline text-base font-bold">{c.title}</h3>
                  <div className="grid gap-3 sm:grid-cols-2">
                    <div className="border-l-2 border-outline-variant pl-3 text-sm text-on-surface-variant">
                      {c.a}
                    </div>
                    <div className="border-l-2 border-outline-variant pl-3 text-sm text-on-surface-variant">
                      {c.b}
                    </div>
                  </div>
                  {c.resolution && (
                    <p className="border-t border-outline-variant pt-3 text-sm">
                      <span className="label text-secondary">Развязка · </span>
                      {c.resolution}
                    </p>
                  )}
                </Card>
              ))}
            </div>
          )}

          {tab === 'пробелы' && (
            <div className="grid gap-4 lg:grid-cols-2">
              {gaps.map((g) => (
                <Card key={g.topic} className="flex items-start justify-between gap-3">
                  <span className="text-sm">{g.topic}</span>
                  <span
                    className={`shrink-0 rounded-full px-2 py-0.5 text-xs font-medium ${
                      priorityTone[g.priority] ?? priorityTone.low
                    }`}
                  >
                    {g.priority}
                  </span>
                </Card>
              ))}
            </div>
          )}

          {tab === 'граф' && (
            <div className="space-y-3">
              <p className="text-sm text-on-surface-variant">
                Алгоритмический: связи категорий через пересекающиеся теги из каталога, пересчёт при
                каждом обращении. Данные — факт.
              </p>
              {graph === null ? (
                <p className="p-8 text-center text-on-surface-variant">Загрузка…</p>
              ) : (
                <KnowledgeGraph graph={graph} labels={stats.category_labels ?? {}} total={stats.total} />
              )}
            </div>
          )}
        </div>

        <aside className="shrink-0 space-y-4 xl:w-80">
          <Card tone="spotlight">
            <Label className="text-kpi-3-sub">KB / Аналитика</Label>
            <div className="mt-3 font-headline text-5xl font-bold tabular-nums">{stats.total}</div>
            <div className="mt-1 text-xs opacity-70">записи · {clusters} кластеров</div>
            <div className="mt-4 grid grid-cols-2 gap-px bg-current/10">
              {[
                [clusters, 'кластеров'],
                [patterns.length, 'паттернов'],
                [contradictions.length, 'противоречий'],
                [gaps.length, 'пробелов'],
              ].map(([n, label]) => (
                <div key={label} className="py-2">
                  <div className="font-headline text-2xl font-bold tabular-nums">{n}</div>
                  <div className="label opacity-60">{label}</div>
                </div>
              ))}
            </div>
          </Card>

          {config.pull_quote && (
            <Card className="border-l-2 border-l-secondary">
              <blockquote className="font-headline text-sm italic leading-relaxed">
                «{config.pull_quote}»
              </blockquote>
              {config.pull_quote_meta && (
                <p className="label mt-2">{config.pull_quote_meta}</p>
              )}
            </Card>
          )}

          {chain.length > 0 && (
            <Card>
              <Label>Откуда это следует</Label>
              <dl className="mt-3 space-y-2.5">
                {chain.map((l) => (
                  <div key={l.cluster}>
                    <dt className="text-xs font-semibold">{l.cluster}</dt>
                    <dd className="text-xs text-on-surface-variant">{l.evidence}</dd>
                  </div>
                ))}
              </dl>
            </Card>
          )}
        </aside>
      </div>
    </div>
  )
}
