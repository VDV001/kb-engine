import { useState } from 'react'
import { api } from './api'
import type { AnalyticsConfig, Graph, ManifestoQuote, Stats, Support } from './api'
import { useResource } from './hooks/useResource'
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
 * KnowledgeGraph draws the tag-intersection topology: the largest category in
 * the centre, the rest on a circle, edges weighted by shared tags.
 *
 * The layout is deterministic — position comes from the node's rank, not from
 * a simulation — so the same catalog always draws the same picture and a
 * screenshot is comparable with yesterday's.
 */
function KnowledgeGraph({ graph }: { graph: Graph }) {
  const nodes = graph.nodes.slice(0, 24)
  if (nodes.length === 0) return <p className="text-sm text-on-surface-variant">Нет данных.</p>

  const W = 900
  const H = 560
  const cx = W / 2
  const cy = H / 2
  const pos = new Map<string, { x: number; y: number }>()
  pos.set(nodes[0].category, { x: cx, y: cy })
  const ring = nodes.slice(1)
  ring.forEach((n, i) => {
    // Два радиуса вперемежку, чтобы соседние подписи не слипались.
    const r = i % 2 === 0 ? 230 : 165
    const a = (2 * Math.PI * i) / ring.length - Math.PI / 2
    pos.set(n.category, { x: cx + r * Math.cos(a), y: cy + r * Math.sin(a) })
  })

  const maxW = Math.max(1, ...graph.edges.map((e) => e.weight))
  // Сильнейшие 64: живой каталог даёт 200+ рёбер, и все разом — это войлок,
  // а не граф. Рёбра приходят по весу, так что срез — это и есть отбор.
  const edges = graph.edges.filter((e) => pos.has(e.from) && pos.has(e.to)).slice(0, 64)

  return (
    <div className="overflow-x-auto border border-outline-variant bg-surface-lowest">
      <svg viewBox={`0 0 ${W} ${H}`} className="min-w-[40rem]" role="img" aria-label="Граф знаний">
        {edges.map((e) => {
          const a = pos.get(e.from)!
          const b = pos.get(e.to)!
          return (
            <line
              key={`${e.from}-${e.to}`}
              x1={a.x}
              y1={a.y}
              x2={b.x}
              y2={b.y}
              stroke="var(--secondary)"
              strokeWidth={0.5 + (e.weight / maxW) * 2}
              opacity={0.12 + (e.weight / maxW) * 0.35}
            />
          )
        })}
        {nodes.map((n, i) => {
          const p = pos.get(n.category)!
          const centre = i === 0
          const label = n.category.length > 14 ? `${n.category.slice(0, 13)}…` : n.category
          return (
            <g key={n.category}>
              <rect
                x={p.x - 52}
                y={p.y - 16}
                width={104}
                height={32}
                rx={4}
                fill={centre ? 'var(--surface-high)' : 'var(--surface-lowest)'}
                stroke="var(--secondary)"
                strokeOpacity={centre ? 0.9 : 0.45}
              />
              <text
                x={p.x}
                y={p.y - 1}
                textAnchor="middle"
                fill="var(--on-surface)"
                style={{ font: '600 9px var(--font-label)', textTransform: 'uppercase', letterSpacing: '0.06em' }}
              >
                {label}
              </text>
              <text
                x={p.x}
                y={p.y + 11}
                textAnchor="middle"
                fill="var(--on-surface-variant)"
                style={{ font: '9px var(--font-mono)' }}
              >
                {n.count}
              </text>
            </g>
          )
        })}
      </svg>
    </div>
  )
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
  const res = useResource(api.graph, tab === 'граф')
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
                <KnowledgeGraph graph={graph} />
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
