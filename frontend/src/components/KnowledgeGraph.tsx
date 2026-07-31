import type { Entry, Graph } from '../api'
import { categoryLabel } from '../catalog'
import { graphInsights, type LinkInsight } from '../graphInsights'
import { layoutGraph, type GraphNodeBox } from '../graphLayout'

/** Подпись обрезается по числу знаков, а не по ширине: ширина прямоугольника
 * известна заранее, и мерить текст в SVG значит считать то, что уже посчитано. */
function truncate(s: string, max: number): string {
  return s.length > max ? `${s.slice(0, max - 1)}…` : s
}

/**
 * KnowledgeGraph рисует топологию по пересечению тегов: самая крупная категория
 * в центре, остальные двумя эллиптическими кольцами, толщина связи — число
 * общих меток. Рядом нумерованный список: на холсте у узла тот же номер.
 *
 * Кольцо означает связь с ядром, а не размер: размер уже сказан числом на узле
 * и площадью прямоугольника. Ближнее кольцо — темы, сросшиеся с главной линией,
 * дальнее — острова со своим словарём.
 *
 * Раскладка детерминирована и живёт в graphLayout — позиция считается из
 * порядкового номера, а не из симуляции, поэтому одна и та же база даёт одну и
 * ту же картинку, и снимок сравним со вчерашним.
 */
export function KnowledgeGraph({
  graph,
  labels,
  total,
  entries = [],
}: {
  graph: Graph
  labels: Record<string, string>
  total: number
  entries?: Entry[]
}) {
  const W = 900
  const H = 580
  const l = layoutGraph(graph, { width: W, height: H })
  if (l.nodes.length === 0) return <p className="text-sm text-on-surface-variant">Нет данных.</p>

  const name = (key: string) => categoryLabel(key, labels)

  const hub = l.nodes.find((n) => n.isHub)
  const hubGlow = hub ? Math.hypot(hub.width, hub.height) / 2 : 0

  return (
    <div className="space-y-4">
      <div className="overflow-x-auto rounded-xl border border-outline-variant">
        <div className="flex min-w-[48rem]">
          <svg
            viewBox={`0 0 ${l.canvasWidth} ${H}`}
            style={{ width: `${(l.canvasWidth / W) * 100}%` }}
            className="kg-canvas"
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

            {/* Волны от ядра: единственная анимация, которая что-то значит —
                она показывает, откуда расходится база. */}
            {hub &&
              [0, 1.6].map((delay) => (
                <circle key={delay} cx={hub.x} cy={hub.y} r={hubGlow + 4} fill="none" stroke="var(--secondary)" strokeWidth={0.8} opacity={0}>
                  <animate attributeName="r" from={hubGlow + 4} to={hubGlow + 52} dur="3.2s" begin={`${delay}s`} repeatCount="indefinite" calcMode="spline" keySplines="0.4 0 0.6 1" keyTimes="0;1" />
                  <animate attributeName="opacity" from={0.45} to={0} dur="3.2s" begin={`${delay}s`} repeatCount="indefinite" calcMode="spline" keySplines="0.4 0 0.6 1" keyTimes="0;1" />
                </circle>
              ))}

            {l.edges.map((e) => (
              <line
                key={`${e.from}-${e.to}`}
                className={e.strong ? undefined : 'kg-line-flow'}
                x1={e.x1}
                y1={e.y1}
                x2={e.x2}
                y2={e.y2}
                stroke="var(--secondary)"
                strokeWidth={e.strokeWidth}
                opacity={e.opacity}
              />
            ))}

            {l.nodes.map((n) => {
              const x = n.x - n.width / 2
              const y = n.y - n.height / 2
              const large = n.width >= 100
              return (
                <g key={n.key} className="kg-node">
                  <rect
                    x={x}
                    y={y}
                    width={n.width}
                    height={n.height}
                    rx={n.isHub ? 6 : 5}
                    fill={n.isHub ? 'var(--surface-high)' : 'var(--surface-lowest)'}
                    stroke={n.isHub ? 'currentColor' : 'var(--secondary)'}
                    strokeOpacity={n.isHub ? 0.1 : n.ring === 'inner' ? 0.75 : 0.4}
                  />
                  {/* Внутренняя пунктирная рамка — та самая деталь, из-за которой
                      узел выглядит чертежом, а не прямоугольником. */}
                  <rect
                    className="kg-inner-dashed"
                    x={x + (n.isHub ? 5 : 4)}
                    y={y + (n.isHub ? 5 : 4)}
                    width={n.width - (n.isHub ? 10 : 8)}
                    height={n.height - (n.isHub ? 10 : 8)}
                    rx={3}
                    fill="none"
                    stroke={n.isHub ? 'currentColor' : 'var(--secondary)'}
                    strokeOpacity={n.isHub ? 0.18 : 0.35}
                    strokeDasharray="3 3"
                  />

                  {n.isHub ? (
                    <>
                      <text x={n.x} y={y + 22} textAnchor="middle" className="font-label" fontSize={6.5} fill="currentColor" opacity={0.38} letterSpacing={1.2}>
                        CORE
                      </text>
                      <text x={n.x} y={y + 40} textAnchor="middle" fill="var(--on-surface)" style={{ font: '800 10px var(--font-sans)', letterSpacing: '-0.01em' }}>
                        {truncate(name(n.key).toUpperCase(), 14)}
                      </text>
                      <text x={n.x} y={y + 56} textAnchor="middle" fill="var(--secondary)" opacity={0.7} style={{ font: '600 7.5px var(--font-label)' }}>
                        {n.count} entries
                      </text>
                    </>
                  ) : (
                    <>
                      <text x={x + 10} y={y + 16} className="font-label" fontSize={6.5} fill="var(--secondary)" opacity={0.5} letterSpacing={1.2}>
                        {String(n.index).padStart(2, '0')}
                      </text>
                      <text x={x + 10} y={y + n.height / 2 + 8} fill="var(--on-surface)" style={{ font: `700 ${large ? 9 : 8}px var(--font-sans)`, letterSpacing: '0.02em' }}>
                        {truncate(name(n.key).toUpperCase(), large ? 13 : 10)}
                      </text>
                      {large && (
                        <line x1={x + 10} y1={y + n.height - 12} x2={x + n.width - 10} y2={y + n.height - 12} stroke="var(--secondary)" strokeOpacity={0.18} strokeWidth={1} />
                      )}
                      <text x={x + n.width - 10} y={y + 16} textAnchor="end" className="font-label" fontSize={7} fill="var(--on-surface-variant)" opacity={0.55}>
                        {n.count}
                      </text>
                    </>
                  )}
                </g>
              )
            })}
          </svg>

          <NodeIndex nodes={l.nodes} labels={labels} total={total} width={(l.sideWidth / W) * 100} />
        </div>
      </div>

      <GraphLegend />
      <GraphConclusions graph={graph} labels={labels} total={total} entries={entries} />
    </div>
  )
}

/**
 * NodeIndex — боковая панель холста: семь крупнейших тем с долей от базы.
 * На холсте подписи обрезаны по ширине прямоугольника, и полное имя видно
 * только здесь.
 */
function NodeIndex({
  nodes,
  labels,
  total,
  width,
}: {
  nodes: GraphNodeBox[]
  labels: Record<string, string>
  total: number
  width: number
}) {
  const top = [...nodes].sort((a, b) => b.count - a.count).slice(0, 7)

  return (
    <aside
      className="flex shrink-0 flex-col border-l border-outline-variant bg-surface-lowest/65 p-5 backdrop-blur"
      style={{ width: `${width}%` }}
    >
      <div className="mb-6 flex items-center gap-2">
        <span className="size-1.5 shrink-0 rounded-full bg-secondary" />
        <span className="font-label text-[8px] font-semibold uppercase tracking-[0.35em] text-on-surface-variant">
          Node index
        </span>
      </div>

      <ul className="flex flex-1 flex-col gap-4">
        {top.map((n, i) => {
          const pct = total > 0 ? Math.round((n.count / total) * 100) : 0
          return (
            <li key={n.key}>
              <span className="block font-label text-[7px] tracking-[1.5px] text-on-surface-variant opacity-40">
                {String(i + 1).padStart(2, '0')}
              </span>
              <p
                className={`my-0.5 truncate text-[10px] font-bold uppercase tracking-[0.04em] ${
                  i === 0 ? 'text-secondary' : 'text-on-surface'
                }`}
                title={`${labels[n.key] || n.key} · ${n.count} записей`}
              >
                {name2(n.key, labels)}
              </p>
              {/* Полоска — доля от базы. Число рядом не ставим: их семь, и
                  колонка цифр читалась бы как таблица, а это подпись к холсту. */}
              <div className="h-px bg-outline-variant">
                <div className="h-px bg-secondary opacity-60" style={{ width: `${pct}%` }} />
              </div>
            </li>
          )
        })}
      </ul>

      <div className="mt-5 bg-surface-high p-3.5">
        <p className="font-label text-[7px] uppercase tracking-[0.22em] text-on-surface-variant opacity-55">
          Technical summary
        </p>
        <p className="mt-1.5 text-[9px] leading-relaxed text-on-surface opacity-55">
          {nodes.length} категорий · {total} записей.
          <br />
          Граф пересчитывается при каждом обращении.
        </p>
      </div>
    </aside>
  )
}

function name2(key: string, labels: Record<string, string>): string {
  return categoryLabel(key, labels)
}

/** Без этой подписи кольца — просто украшение: расстояние до центра ничего не
 * говорит само по себе, пока не сказано, что оно измеряет. */
function GraphLegend() {
  return (
    <dl className="grid gap-2 text-xs sm:grid-cols-3">
      {[
        ['В центре', 'ядро базы — тема, вокруг которой собрано остальное'],
        ['Ближе к центру', 'больше общих тегов с ядром: грани одной линии'],
        ['С краю', 'свой словарь, почти нет пересечений — острова'],
      ].map(([term, desc]) => (
        <div key={term} className="border border-outline-variant bg-surface-lowest p-3">
          <dt className="label mb-1">{term}</dt>
          <dd className="text-on-surface-variant">{desc}</dd>
        </div>
      ))}
    </dl>
  )
}

/**
 * GraphConclusions — то, ради чего на граф смотрят. Картинка показывает форму,
 * но вывод из неё читатель иначе делает на глаз и каждый раз заново.
 */
export function GraphConclusions({
  graph,
  labels,
  total,
  entries = [],
}: {
  graph: Graph
  labels: Record<string, string>
  total: number
  entries?: Entry[]
}) {
  const i = graphInsights(layoutGraph(graph, { width: 900, height: 580 }), { total, entries })
  if (!i.core) return null
  const name = (key: string) => categoryLabel(key, labels)

  return (
    <div className="grid gap-3 lg:grid-cols-3">
      <div className="flex flex-col border border-outline-variant bg-surface-lowest p-5">
        <p className="label mb-3">Ядро базы</p>
        <p className="font-headline text-xl font-bold leading-tight">{name(i.core.key)}</p>
        <dl className="mt-4 space-y-2 text-sm">
          <Row term="Записей" value={`${i.core.count} · ${i.core.share}% базы`} />
          <Row term="Свой словарь" value={`${i.core.vocabulary} тегов`} />
          <Row term="Тесно связаны" value={`${i.core.closeCount} из ${i.core.peerCount} категорий`} />
        </dl>
        <p className="mt-auto pt-4 text-xs text-on-surface-variant">
          Тесная связь — от одного общего тега на запись. Остальные делят с ядром отдельные метки, а
          не словарь.
        </p>
      </div>

      <LinkCard
        title="Срослось с ядром"
        items={i.fused}
        name={name}
        note="Больше всего общих тегов на запись — на деле одна область."
        empty="Категорий пока слишком мало, чтобы отделить плотные от разреженных."
        tagsOf={(it) => it.sharedTags}
        tagsLabel="общее"
      />

      <LinkCard
        title="Острова"
        items={i.islands}
        name={name}
        note="Меньше всего общих тегов на запись: свой словарь. Развивать — или пометить dead-end."
        empty="Разреженных тем нет."
        tagsOf={(it) => it.ownTags}
        tagsLabel="только у неё"
      />
    </div>
  )
}

function Row({ term, value }: { term: string; value: string }) {
  return (
    <div className="flex items-baseline justify-between gap-3">
      <dt className="shrink-0 text-on-surface-variant">{term}</dt>
      <dd className="text-right font-mono text-xs tabular-nums">{value}</dd>
    </div>
  )
}

/** Обе карточки — один список с разного конца, и различаются они подписью, а
 * не устройством. */
function LinkCard({
  title,
  items,
  name,
  note,
  empty,
  tagsOf,
  tagsLabel,
}: {
  title: string
  items: LinkInsight[]
  name: (key: string) => string
  note: string
  empty: string
  tagsOf: (it: LinkInsight) => string[]
  tagsLabel: string
}) {
  return (
    <div className="flex flex-col border border-outline-variant bg-surface-lowest p-5">
      <p className="label mb-3">{title}</p>
      {items.length === 0 ? (
        <p className="text-sm text-on-surface-variant">{empty}</p>
      ) : (
        <>
          <ul className="space-y-3">
            {items.map((it) => {
              const tags = tagsOf(it)
              return (
                <li key={it.key}>
                  {/* Название и число на одной строке разъезжались: длинное имя
                      выталкивало «тег/запись» на следующую строку. Теперь имя
                      занимает строку целиком, число уходит под него. */}
                  <p className="truncate text-sm" title={name(it.key)}>
                    {name(it.key)}
                  </p>
                  <p className="mt-0.5 font-mono text-[11px] text-on-surface-variant tabular-nums">
                    {it.perEntry.toFixed(1)} тег/запись · {it.count} записей
                  </p>
                  {tags.length > 0 && (
                    <p className="mt-1 text-xs text-on-surface-variant">
                      <span className="opacity-60">{tagsLabel}: </span>
                      {tags.join(', ')}
                    </p>
                  )}
                </li>
              )
            })}
          </ul>
          <p className="mt-auto pt-4 text-xs text-on-surface-variant">{note}</p>
        </>
      )}
    </div>
  )
}
