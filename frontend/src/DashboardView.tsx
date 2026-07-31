import { useState } from 'react'
import { api } from './api'
import type { Entry, Graph, Stats } from './api'
import { buildActivity, DAY_LABELS } from './activity'
import { categoryLabel, statusOf, topTags } from './catalog'
import { KnowledgeGraph } from './components/KnowledgeGraph'
import { useResource } from './hooks/useResource'

/** Сколько недель показывает лента активности. */
const WEEKS = 21

/** Сколько дней в столбчатом графике: месяц — тот горизонт, на котором ещё
 * различимы отдельные дни. */
const BAR_DAYS = 30

/** Верхушка облака. Хвост базы — теги-одиночки, их тут тысячи. */
const CLOUD_TAGS = 80

export function DashboardView({ stats, entries }: { stats: Stats; entries: Entry[] }) {
  const graphRes = useResource(api.graph)
  const graph: Graph | null =
    graphRes.status === 'ready'
      ? graphRes.data
      : graphRes.status === 'failed'
        ? { nodes: [], edges: [] }
        : null

  const labels = stats.category_labels ?? {}
  const activity = buildActivity(entries, { weeks: WEEKS, today: new Date() })
  const days = activity.columns.flatMap((c) => c.days).filter((d) => !d.isFuture)
  const recent = days.slice(-BAR_DAYS)
  const categories = Object.entries(stats.by_category).sort((a, b) => b[1] - a[1])
  const fromBot = entries.filter((e) => e.source === 'bot-inbox').length

  // Темп: последние 30 дней против предыдущих 30. Оба окна берутся из той же
  // ленты, что нарисована ниже, — иначе цифра и картинка разойдутся.
  const lastMonth = sum(days.slice(-BAR_DAYS))
  const prevMonth = sum(days.slice(-BAR_DAYS * 2, -BAR_DAYS))
  const perWeek = Math.round((lastMonth / BAR_DAYS) * 7)

  return (
    <div className="space-y-12">
      <header>
        <h1 className="font-headline text-4xl font-bold tracking-tighter md:text-5xl">База знаний.</h1>
        <p className="mt-2 max-w-3xl text-on-surface-variant">
          Живая память об AI-разработке: записи, категории и активность добавления. У каждой записи —
          lifecycle-статус, поэтому база не гниёт, а стареет управляемо.
        </p>
      </header>

      <div className="grid gap-6 md:grid-cols-3">
        <Kpi label="Всего записей" value={stats.total} bg="bg-kpi-1-bg">
          {categories.length} категорий · {stats.by_lifecycle['canonical'] ?? 0} canonical
        </Kpi>
        {/* Не «Категорий»: их число и крупнейшую тему уже говорит донат ниже.
            Темп — единственный вопрос, на который на дашборде нет ответа числом:
            лента активности показывает форму, но не итог. */}
        <Kpi label="Добавлено за месяц" value={lastMonth} bg="bg-kpi-2-bg">
          {perWeek} в неделю ·{' '}
          {prevMonth === 0
            ? 'прошлый месяц пустой'
            : `${lastMonth >= prevMonth ? '+' : ''}${Math.round(((lastMonth - prevMonth) / prevMonth) * 100)}% к прошлому`}
        </Kpi>
        <Kpi label="Из Telegram бота" value={fromBot} bg="bg-kpi-3-bg" tone="text-kpi-3-text">
          {stats.total > 0 ? Math.round((fromBot / stats.total) * 100) : 0}% базы приехало ботом
        </Kpi>
      </div>

      <div className="grid gap-6 md:grid-cols-12">
        <Panel className="md:col-span-8" title="Записи по дням" subtitle={`Появление записей за последние ${BAR_DAYS} дней`}>
          <DayBars days={recent} />
        </Panel>

        <Panel className="md:col-span-4" title="Категории" subtitle="Распределение по темам">
          <Donut categories={categories} labels={labels} total={stats.total} />
        </Panel>

        <Panel className="md:col-span-12" title="Статусы записей" subtitle="Непрочитанные, отложенные и разобранные">
          <StatusBars entries={entries} />
        </Panel>
      </div>

      <Section
        title="Активность"
        subtitle={`Появление записей по дням за ${WEEKS} недель. Всплеск — день разбора партии, а не ежедневный ритм`}
        aside={<Legend max={activity.maxCount} />}
      >
        <Heatmap activity={activity} />
      </Section>

      <TagCloud entries={entries} />

      <Section title="Граф знаний" subtitle="Связи категорий через общие теги. Пересчёт при каждом обращении — данные факт">
        {graph === null ? (
          <p className="p-8 text-center text-on-surface-variant">Загрузка…</p>
        ) : (
          <KnowledgeGraph graph={graph} labels={labels} total={stats.total} entries={entries} />
        )}
      </Section>
    </div>
  )
}

function Kpi({
  label,
  value,
  bg,
  tone = '',
  children,
}: {
  label: string
  value: number
  bg: string
  tone?: string
  children?: React.ReactNode
}) {
  return (
    <div className={`border border-outline-variant p-8 ${bg} ${tone}`}>
      <p className="label mb-4">{label}</p>
      <p className="font-headline text-5xl font-bold tracking-tighter tabular-nums">{value}</p>
      <p className="mt-3 text-sm opacity-70">{children}</p>
    </div>
  )
}

function Panel({
  title,
  subtitle,
  className = '',
  children,
}: {
  title: string
  subtitle: string
  className?: string
  children: React.ReactNode
}) {
  return (
    <div className={`flex flex-col border border-outline-variant bg-surface-lowest p-8 ${className}`}>
      <h4 className="font-headline text-lg font-bold tracking-tight">{title}</h4>
      <p className="mb-8 text-sm text-on-surface-variant">{subtitle}</p>
      {children}
    </div>
  )
}

function Section({
  title,
  subtitle,
  aside,
  children,
}: {
  title: string
  subtitle: string
  aside?: React.ReactNode
  children: React.ReactNode
}) {
  return (
    <section className="border-t border-outline-variant pt-12">
      <div className="mb-6 flex flex-wrap items-end justify-between gap-4">
        <div>
          <h3 className="font-headline text-xl font-bold tracking-tight">{title}</h3>
          <p className="mt-1 max-w-2xl text-xs text-on-surface-variant">{subtitle}</p>
        </div>
        {aside}
      </div>
      {children}
    </section>
  )
}

/** Столбики по дням. Высота — доля от самого урожайного дня в окне. */
function DayBars({ days }: { days: { date: string; count: number }[] }) {
  const max = Math.max(1, ...days.map((d) => d.count))
  return (
    <div className="flex h-[200px] flex-1 items-end gap-1">
      {days.map((d) => (
        <div
          key={d.date}
          className="group relative flex-1 bg-secondary"
          style={{ height: `${Math.max((d.count / max) * 100, d.count > 0 ? 3 : 1)}%`, opacity: d.count > 0 ? 0.85 : 0.15 }}
          title={`${d.date}: ${d.count}`}
        />
      ))}
    </div>
  )
}

/**
 * Настоящий донат по долям категорий — в исходном дашборде кольцо было
 * декоративным: два цвета фиксированной толщины, не связанные с данными.
 */
function Donut({
  categories,
  labels,
  total,
}: {
  categories: [string, number][]
  labels: Record<string, string>
  total: number
}) {
  const top = categories.slice(0, 5)
  const shown = top.reduce((sum, [, n]) => sum + n, 0)
  const rest = total - shown
  const slices = rest > 0 ? [...top, ['—прочее', rest] as [string, number]] : top

  const R = 60
  const C = 2 * Math.PI * R
  let offset = 0

  return (
    <div className="flex flex-1 flex-col items-center">
      <svg viewBox="0 0 160 160" className="h-48 w-48 -rotate-90">
        {slices.map(([key, n], i) => {
          const len = total > 0 ? (n / total) * C : 0
          const dash = `${len} ${C - len}`
          const el = (
            <circle
              key={key}
              cx={80}
              cy={80}
              r={R}
              fill="none"
              stroke="var(--secondary)"
              strokeWidth={16}
              strokeDasharray={dash}
              strokeDashoffset={-offset}
              opacity={1 - i * 0.15}
            />
          )
          offset += len
          return el
        })}
      </svg>
      <p className="-mt-28 font-headline text-2xl font-bold tabular-nums">{categories.length}</p>
      <p className="label mt-16">категорий</p>

      <ul className="mt-8 w-full space-y-2 text-sm">
        {slices.map(([key, n], i) => (
          <li key={key} className="flex items-baseline gap-2">
            <span className="size-2.5 shrink-0 bg-secondary" style={{ opacity: 1 - i * 0.15 }} />
            <span className="min-w-0 flex-1 truncate" title={key === '—прочее' ? 'прочее' : categoryLabel(key, labels)}>
              {key === '—прочее' ? 'прочее' : categoryLabel(key, labels)}
            </span>
            {/* И доля, и само число: процент отвечает «насколько велика тема»,
                счётчик — «сколько там читать». */}
            <span className="shrink-0 font-mono text-xs text-on-surface-variant tabular-nums">{n}</span>
            <span className="w-10 shrink-0 text-right font-mono text-xs font-bold tabular-nums">
              {total > 0 ? Math.round((n / total) * 100) : 0}%
            </span>
          </li>
        ))}
      </ul>
    </div>
  )
}

/** Статусы считаются через statusOf — тот же разбор, что в архиве, поэтому
 * цифры на двух экранах сходятся. */
function StatusBars({ entries }: { entries: Entry[] }) {
  const counts = new Map<string, { label: string; n: number }>()
  for (const e of entries) {
    const s = statusOf(e)
    const cur = counts.get(s.key)
    counts.set(s.key, { label: s.label, n: (cur?.n ?? 0) + 1 })
  }
  const bars = [...counts.entries()].sort((a, b) => b[1].n - a[1].n)
  const max = Math.max(1, ...bars.map(([, v]) => v.n))

  return (
    <div className="grid flex-1 grid-cols-2 items-end gap-4 sm:grid-cols-4">
      {bars.map(([key, v]) => (
        <div key={key} className="flex h-32 flex-col justify-end">
          <span className="mb-1 text-center font-mono text-xs tabular-nums text-on-surface-variant">{v.n}</span>
          <div className="w-full bg-secondary" style={{ height: `${(v.n / max) * 100}%`, opacity: v.n === max ? 1 : 0.7 }} />
          <span className="mt-2 truncate text-center text-[10px] text-on-surface-variant" title={v.label}>
            {v.label}
          </span>
        </div>
      ))}
    </div>
  )
}

function Legend({ max }: { max: number }) {
  return (
    <div className="flex items-center gap-2 text-[10px] text-on-surface-variant">
      <span>Меньше</span>
      <div className="flex gap-1">
        {[0, 1, 2, 3, 4].map((l) => (
          <span key={l} className="size-4 rounded-sm" style={cellStyle(l)} />
        ))}
      </div>
      <span>Больше {max > 0 ? `(до ${max})` : ''}</span>
    </div>
  )
}

/** Один тон разной плотности: цвет здесь означает количество, и вторая краска
 * читалась бы как вторая величина. */
function cellStyle(level: number): React.CSSProperties {
  if (level === 0) return { background: 'var(--surface-high)' }
  return { background: 'var(--secondary)', opacity: 0.25 + level * 0.19 }
}

function Heatmap({ activity }: { activity: ReturnType<typeof buildActivity> }) {
  return (
    <div className="overflow-x-auto">
      <div className="flex min-w-[42rem] gap-1">
        <div className="flex shrink-0 flex-col gap-1 pr-1.5">
          {DAY_LABELS.map((l, i) => (
            <span key={i} className="h-5 text-[10px] leading-5 text-on-surface-variant">
              {l}
            </span>
          ))}
        </div>
        {activity.columns.map((col, i) => (
          <div key={i} className="flex flex-1 flex-col gap-1">
            {col.days.map((d) => (
              <span
                key={d.date}
                className="h-5 rounded-sm"
                style={d.isFuture ? { background: 'transparent' } : cellStyle(d.level)}
                title={`${d.date}: ${d.count}`}
              />
            ))}
          </div>
        ))}
      </div>
    </div>
  )
}

/** Облако свёрнуто по умолчанию: развёрнутое оно занимает экран целиком, а
 * читают его редко. */
function TagCloud({ entries }: { entries: Entry[] }) {
  const [open, setOpen] = useState(false)
  const tags = topTags(entries, CLOUD_TAGS)

  return (
    <Section
      title="Облако тегов"
      subtitle={`${CLOUD_TAGS} самых частых из ${countTags(entries)} тегов базы`}
      aside={
        <button
          type="button"
          onClick={() => setOpen((v) => !v)}
          className="border border-outline-variant px-4 py-2 text-xs font-bold hover:bg-surface-high"
          aria-expanded={open}
        >
          {open ? 'Свернуть' : 'Развернуть'}
        </button>
      }
    >
      {open && (
        <div className="flex flex-wrap justify-center gap-3">
          {tags.map((t) => (
            <span
              key={t.tag}
              className="border border-outline-variant px-2 py-1 text-on-surface"
              style={{ fontSize: `${0.75 + t.scale * 0.9}rem`, opacity: 0.55 + t.scale * 0.45 }}
              title={`${t.count} записей`}
            >
              {t.tag}
            </span>
          ))}
        </div>
      )}
    </Section>
  )
}

function countTags(entries: Entry[]): number {
  const seen = new Set<string>()
  for (const e of entries) for (const t of e.tags ?? []) seen.add(t)
  return seen.size
}

function sum(days: { count: number }[]): number {
  return days.reduce((n, d) => n + d.count, 0)
}
