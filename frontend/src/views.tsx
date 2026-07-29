import { useMemo, useState } from 'react'
import type { Analytics, AnalyticsConfig, Audits, DuplicateGroup, Entry, Finances, Finding, Stats, Transaction } from './api'
import { Badge, BarList, Card, Ring, Section, Stat } from './components/ui'
import {
  daysOfMonth,
  formatRub,
  monthLabel,
  monthOf,
  monthsBetween,
  sumBy,
  sumByAccount,
  toKopecks,
  plural,
  toRoubleBars,
} from './money'

export function OverviewView({ stats }: { stats: Stats }) {
  // The spotlight card is the one the eye lands on, so it carries the most
  // telling figure rather than whichever happened to be fourth. A zero in the
  // loudest slot tells the reader nothing and wastes the emphasis.
  const categories = Object.entries(stats.by_category).sort((a, b) => b[1] - a[1])
  const [topCategory, topCount] = categories[0] ?? ['—', 0]
  const share = stats.total > 0 ? Math.round((topCount / stats.total) * 100) : 0

  return (
    <div className="space-y-8">
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-4">
        <Stat label="Всего записей" value={stats.total} />
        <Stat label="Категорий" value={Object.keys(stats.by_category).length} />
        <Stat label="Canonical" value={stats.by_lifecycle['canonical'] ?? 0} tone="muted" />
        <Stat
          label="Топ категория"
          value={topCount}
          tone="spotlight"
          hint={`${topCategory} · ${share}% каталога`}
        />
      </div>
      <Section title="Распределение по категориям" subtitle="Доля восьми крупнейших от каталога">
        <div className="grid grid-cols-2 divide-x divide-y divide-outline-variant border border-outline-variant sm:grid-cols-4 xl:grid-cols-8">
          {categories.slice(0, 8).map(([name, n]) => (
            <Ring
              key={name}
              label={name}
              percent={stats.total > 0 ? Math.round((n / stats.total) * 100) : 0}
            />
          ))}
        </div>
      </Section>

      <div className="grid gap-4 lg:grid-cols-2">
        <Card>
          <Section title="По категориям">
            <BarList data={stats.by_category} />
          </Section>
        </Card>
        <div className="space-y-4">
          <Card>
            <Section title="По жизненному циклу">
              <BarList data={stats.by_lifecycle} />
            </Section>
          </Card>
          <Card>
            <Section title="По вердикту">
              <BarList data={stats.by_verdict} />
            </Section>
          </Card>
        </div>
      </div>
    </div>
  )
}

export function EntriesView({ entries }: { entries: Entry[] }) {
  const [search, setSearch] = useState('')
  const [category, setCategory] = useState('')

  const categories = useMemo(
    () => Array.from(new Set(entries.map((e) => e.category))).sort(),
    [entries],
  )

  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase()
    return entries.filter(
      (e) =>
        (category === '' || e.category === category) &&
        (q === '' || e.title.toLowerCase().includes(q)),
    )
  }, [entries, search, category])

  return (
    <Section title="Записи" subtitle={`${filtered.length} из ${entries.length}`}>
      <div className="flex flex-wrap gap-2">
        <input
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          placeholder="Поиск по названию…"
          className="flex-1 rounded-lg border border-outline-variant px-3 py-1.5 text-sm"
        />
        <select
          value={category}
          onChange={(e) => setCategory(e.target.value)}
          className="rounded-lg border border-outline-variant px-3 py-1.5 text-sm"
        >
          <option value="">Все категории</option>
          {categories.map((c) => (
            <option key={c} value={c}>
              {c}
            </option>
          ))}
        </select>
      </div>
      <Card className="overflow-x-auto p-0">
        <table className="w-full text-left text-sm">
          <thead className="border-b border-outline-variant text-on-surface-variant">
            <tr>
              <th className="p-2">id</th>
              <th className="p-2">Название</th>
              <th className="p-2">Категория</th>
              <th className="p-2">Цикл</th>
              <th className="p-2">Вердикт</th>
            </tr>
          </thead>
          <tbody>
            {filtered.slice(0, 300).map((e) => (
              <tr key={e.id} className="border-b border-outline-variant/50">
                <td className="p-2 tabular-nums text-on-surface-variant">{e.id}</td>
                <td className="p-2">
                  {e.url ? (
                    <a href={e.url} target="_blank" rel="noreferrer" className="text-secondary hover:underline">
                      {e.title}
                    </a>
                  ) : (
                    e.title
                  )}
                </td>
                <td className="p-2 text-on-surface-variant">{e.category}</td>
                <td className="p-2"><Badge value={e.lifecycle} /></td>
                <td className="p-2">{e.verdict ? <Badge value={e.verdict} /> : <span className="text-on-surface-variant">—</span>}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </Card>
      {filtered.length > 300 && (
        <p className="text-xs text-on-surface-variant">Показаны первые 300 — уточните фильтр.</p>
      )}
    </Section>
  )
}

function FindingsList({ title, findings }: { title: string; findings: Finding[] | null }) {
  const items = findings ?? []
  return (
    <Card>
      <Section title={`${title} (${items.length})`}>
        {items.length === 0 ? (
          <p className="text-sm text-on-surface-variant">Нет кандидатов.</p>
        ) : (
          <ul className="space-y-1.5">
            {items.map((f) => (
              <li key={f.EntryID} className="flex flex-wrap items-center gap-2 text-sm">
                <span className="tabular-nums text-on-surface-variant">#{f.EntryID}</span>
                <span className="flex-1 truncate" title={f.Title}>{f.Title}</span>
                {f.Reasons.map((r) => (
                  <span key={r} className="rounded bg-surface-high px-1.5 py-0.5 text-xs text-on-surface-variant">
                    {r}
                  </span>
                ))}
              </li>
            ))}
          </ul>
        )}
      </Section>
    </Card>
  )
}

export function AuditsView({ audits }: { audits: Audits }) {
  return (
    <div className="space-y-4">
      <FindingsList title="Outdated-кандидаты" findings={audits.outdated} />
      <FindingsList title="Canonical-кандидаты" findings={audits.canonical} />
      <FindingsList title="Проблемы supersession" findings={audits.supersession} />
    </div>
  )
}

export function AnalyticsView({
  analytics,
  config,
}: {
  analytics: Analytics
  config: AnalyticsConfig
}) {
  const maxWeek = Math.max(1, ...analytics.growth.map((w) => w.count))
  const totalRecent = analytics.growth.reduce((sum, w) => sum + w.count, 0)
  const categoryData = Object.fromEntries(analytics.categories.map((c) => [c.category, c.count]))
  const patterns = config.patterns ?? []
  const contradictions = config.contradictions ?? []
  const gaps = config.gaps ?? []
  const quotes = config.manifesto_quotes ?? []

  return (
    <div className="space-y-6">
      <Section title="Аналитика" subtitle="Динамика и распределение базы знаний">
        <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
          <Stat label="Категорий" value={analytics.categories.length} />
          <Stat label="Записей за окно" value={totalRecent} />
          <Stat label="Недель в окне" value={analytics.growth.length} tone="muted" />
          <Stat label="Пик/неделю" value={maxWeek} tone="spotlight" />
        </div>
      </Section>

      <Card>
        <h3 className="mb-3 text-sm font-semibold text-on-surface">
          Рост по неделям (по дате создания)
        </h3>
        <div className="flex items-end gap-1" style={{ height: 140 }}>
          {analytics.growth.map((w) => (
            <div key={w.week} className="flex flex-1 flex-col items-center justify-end gap-1">
              <span className="text-[10px] tabular-nums text-on-surface-variant">{w.count}</span>
              <div
                className="w-full rounded-t bg-donut-primary"
                style={{ height: `${(w.count / maxWeek) * 100}%`, minHeight: w.count > 0 ? 2 : 0 }}
                title={`${w.week}: ${w.count}`}
              />
              <span className="text-[10px] text-on-surface-variant">{w.week}</span>
            </div>
          ))}
        </div>
      </Card>

      <Card>
        <h3 className="mb-3 text-sm font-semibold text-on-surface">Размеры категорий</h3>
        <BarList data={categoryData} />
      </Card>

      {quotes.length > 0 && (
        <Section title="Манифест" subtitle={`${quotes.length} тезисов`}>
          <div className="space-y-2">
            {quotes.map((q, i) => (
              <Card key={i}>
                <p className="text-sm italic text-on-surface">«{q.quote}»</p>
                <p className="mt-1 text-xs text-on-surface-variant">
                  {q.source} · {q.date} {q.weight && <Badge value={q.weight} />}
                </p>
              </Card>
            ))}
          </div>
        </Section>
      )}

      {patterns.length > 0 && (
        <Section title="Паттерны" subtitle={`${patterns.length} сквозных тем`}>
          <div className="space-y-2">
            {patterns.map((p, i) => (
              <Card key={i}>
                <div className="flex flex-wrap items-center gap-2">
                  <span className="text-sm font-semibold text-on-surface">{p.name}</span>
                  {(p.clusters ?? []).map((c) => (
                    <Badge key={c} value={c} />
                  ))}
                </div>
                {p.desc && <p className="mt-1 text-xs text-on-surface-variant">{p.desc}</p>}
              </Card>
            ))}
          </div>
        </Section>
      )}

      {contradictions.length > 0 && (
        <Section title="Противоречия" subtitle={`${contradictions.length}`}>
          <div className="space-y-2">
            {contradictions.map((c, i) => (
              <Card key={i}>
                <p className="text-sm font-semibold text-on-surface">{c.title}</p>
                <p className="mt-1 text-xs text-on-surface-variant">A: {c.a}</p>
                <p className="text-xs text-on-surface-variant">B: {c.b}</p>
                {c.resolution && (
                  <p className="mt-1 text-xs text-secondary">→ {c.resolution}</p>
                )}
              </Card>
            ))}
          </div>
        </Section>
      )}

      {gaps.length > 0 && (
        <Section title="Пробелы" subtitle={`${gaps.length} тем`}>
          <div className="space-y-2">
            {gaps.map((g, i) => (
              <Card key={i}>
                <div className="flex items-center gap-2">
                  <Badge value={g.priority} />
                  <span className="text-sm text-on-surface">{g.topic}</span>
                </div>
              </Card>
            ))}
          </div>
        </Section>
      )}
    </div>
  )
}

const archivedLifecycles = ['outdated', 'superseded', 'dead-end']

export function ArchivesView({ entries }: { entries: Entry[] }) {
  const archived = useMemo(
    () => entries.filter((e) => archivedLifecycles.includes(e.lifecycle)),
    [entries],
  )
  return (
    <Section title="Архив" subtitle={`${archived.length} записей (outdated / superseded / dead-end)`}>
      <div className="space-y-2">
        {archived.map((e) => (
          <Card key={e.id}>
            <div className="flex items-center gap-2">
              <Badge value={e.lifecycle} />
              <span className="text-sm text-on-surface">{e.title}</span>
            </div>
            {e.url && (
              <a href={e.url} className="text-xs text-secondary hover:underline">
                {e.url}
              </a>
            )}
          </Card>
        ))}
        {archived.length === 0 && <p className="text-sm text-on-surface-variant">Архив пуст.</p>}
      </div>
    </Section>
  )
}

export function SettingsView({ stats }: { stats: Stats }) {
  return (
    <Section title="Сводка" subtitle="Состав базы знаний">
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
        <Stat label="Всего записей" value={stats.total} />
        <Stat label="Категорий" value={Object.keys(stats.by_category).length} />
        <Stat label="Типов записей" value={Object.keys(stats.by_kind).length} />
        <Stat label="Статусов жизни" value={Object.keys(stats.by_lifecycle).length} />
      </div>
      <div className="mt-4 grid gap-4 sm:grid-cols-2">
        <Card>
          <h3 className="mb-2 text-sm font-semibold text-on-surface">По жизненному циклу</h3>
          <BarList data={stats.by_lifecycle} />
        </Card>
        <Card>
          <h3 className="mb-2 text-sm font-semibold text-on-surface">По типу</h3>
          <BarList data={stats.by_kind} />
        </Card>
      </div>
    </Section>
  )
}

export function DuplicatesView({ groups }: { groups: DuplicateGroup[] }) {
  return (
    <Section title="Дубликаты" subtitle={`${groups.length} групп`}>
      <div className="space-y-2">
        {groups.map((g, i) => (
          <Card key={i}>
            <div className="flex items-center gap-2 text-sm">
              <Badge value={g.Kind} />
              <span className="tabular-nums text-on-surface-variant">ids: {g.EntryIDs.join(', ')}</span>
            </div>
            <div className="mt-1 truncate text-xs text-on-surface-variant" title={g.Key}>{g.Key}</div>
          </Card>
        ))}
      </div>
    </Section>
  )
}

// --- Finances ---------------------------------------------------------------

// Trend renders the shape of spending over the period: by day when a month is
// picked, by month otherwise. The Python dashboard always showed the last seven
// days; with a month picker on the same screen, matching the picker is the same
// idea applied consistently.
function Trend({ points, masked }: { points: { label: string; kopecks: number }[]; masked: boolean }) {
  if (points.length === 0) return null
  const max = Math.max(1, ...points.map((p) => p.kopecks))
  return (
    <div className="flex items-end gap-1" style={{ height: 64 }}>
      {points.map((p) => (
        <div
          key={p.label}
          // No amount in the tooltip while masked: filter: blur does not reach a
          // native tooltip, so hovering would read out what the mask hides.
          title={masked ? p.label : `${p.label}: ${formatRub(p.kopecks)}`}
          className="min-w-1 flex-1 rounded-t bg-donut-primary"
          style={{ height: Math.max(Math.round((p.kopecks / max) * 64), 2) }}
        />
      ))}
    </div>
  )
}

export function FinancesView({ finances }: { finances: Finances }) {
  const [month, setMonth] = useState('')
  // Hidden by default, like the Python dashboard: the safe state is the one you
  // land on, not the one you have to remember to switch to.
  const [masked, setMasked] = useState(true)

  const months = useMemo(
    () => Array.from(new Set(finances.transactions.map((t) => monthOf(t.date)))).sort().reverse(),
    [finances.transactions],
  )

  const shown = useMemo(
    () => (month === '' ? finances.transactions : finances.transactions.filter((t) => monthOf(t.date) === month)),
    [finances.transactions, month],
  )

  const expenses = shown.filter((t) => t.kind === 'expense')
  const income = shown.filter((t) => t.kind === 'income')
  // Expenses are summed as recorded, so a refund (a negative expense) comes off
  // the total instead of adding to it — the same arithmetic the CLI report does.
  const spent = expenses.reduce((n, t) => n + toKopecks(t.amount), 0)
  const earned = income.reduce((n, t) => n + toKopecks(t.amount), 0)
  const balance = finances.accounts.reduce((n, a) => n + toKopecks(a.balance), 0)

  // By day inside a picked month, by month over all time. Days with no spending
  // stay in the series as zeroes — a gap is information, and dropping it would
  // make three scattered purchases look like a steady week.
  const trend = useMemo(() => {
    const bucket = month === '' ? (t: Transaction) => monthOf(t.date) : (t: Transaction) => t.date
    const sums = sumBy(expenses, bucket)
    const keys = Object.keys(sums).sort()
    const labels =
      month === ''
        ? monthsBetween(keys[0] ?? '', keys[keys.length - 1] ?? '')
        : daysOfMonth(month)
    return labels.map((label) => ({ label, kopecks: sums[label] ?? 0 }))
  }, [expenses, month])

  const byCategory = sumBy(expenses, (t) => t.category ?? '')
  const byAccount = sumByAccount(expenses)
  const top = Object.entries(byCategory).sort((a, b) => b[1] - a[1])[0]

  if (finances.transactions.length === 0 && finances.accounts.length === 0) {
    return (
      <Section title="Финансы" subtitle="Леджер не подключён">
        <Card>
          <p className="text-sm text-on-surface-variant">
            Запусти <code className="rounded bg-surface-high px-1">kbengine serve</code> с
            флагами <code className="rounded bg-surface-high px-1">--ledger</code> и{' '}
            <code className="rounded bg-surface-high px-1">--from</code>, чтобы увидеть финансы.
          </p>
        </Card>
      </Section>
    )
  }

  return (
    <Section
      title="Финансы"
      subtitle={month === '' ? `${shown.length} записей за всё время` : `${shown.length} записей за ${monthLabel(month)}`}
    >
      <div className={masked ? 'privacy-on space-y-4' : 'space-y-4'}>
      <div className="flex flex-wrap gap-2">
        <select
          value={month}
          onChange={(e) => setMonth(e.target.value)}
          className="rounded-lg border border-outline-variant px-3 py-1.5 text-sm"
        >
          <option value="">За всё время</option>
          {months.map((m) => (
            <option key={m} value={m}>
              {monthLabel(m)}
            </option>
          ))}
        </select>
        <button
          onClick={() => setMasked((v) => !v)}
          className="rounded-lg border border-outline-variant px-3 py-1.5 text-sm text-on-surface-variant hover:bg-surface-high dark:hover:bg-surface-high"
        >
          {masked ? 'Показать суммы' : 'Скрыть суммы'}
        </button>
      </div>

      <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
        <Stat label="Баланс по счетам" value={<span className="privacy-mask">{formatRub(balance)}</span>} />
        <Stat label={`Расходы (${expenses.length})`} value={<span className="privacy-mask">{formatRub(spent)}</span>} />
        <Stat label={`Доходы (${income.length})`} value={<span className="privacy-mask">{formatRub(earned)}</span>} />
        <Stat label="Разница" value={<span className="privacy-mask">{formatRub(earned - spent)}</span>} />
      </div>

      <Card>
        <div className="mb-2 flex items-baseline justify-between">
          <h3 className="text-sm font-semibold text-on-surface">
            {month === '' ? 'Расходы по месяцам' : 'Расходы по дням'}
          </h3>
          <span className="text-xs text-on-surface-variant">{plural(trend.length, 'точка', 'точки', 'точек')}</span>
        </div>
        <Trend points={trend} masked={masked} />
      </Card>

      {top && (
        <Card>
          <div className="text-sm text-on-surface-variant">Топ категория</div>
          <div className="mt-1 flex items-baseline gap-2">
            <span className="text-2xl font-bold text-on-surface">{top[0]}</span>
            <span className="privacy-mask tabular-nums text-on-surface-variant">{formatRub(top[1])}</span>
            <span className="text-sm text-on-surface-variant">
              {/* A share of a negative total is not a share of anything: when
                  refunds outweigh purchases the percentage flips sign and reads
                  as nonsense. */}
              {spent <= 0 ? '—' : `${Math.round((top[1] / spent) * 100)}% расходов`}
            </span>
          </div>
        </Card>
      )}

      <div className="grid gap-4 lg:grid-cols-2">
        <Card>
          <h3 className="mb-2 text-sm font-semibold text-on-surface">Расходы по категориям, ₽</h3>
          <BarList data={toRoubleBars(byCategory)} valueClassName="privacy-mask" />
        </Card>
        <div className="space-y-4">
          <Card>
            <h3 className="mb-2 text-sm font-semibold text-on-surface">Расходы по счетам, ₽</h3>
            <BarList data={toRoubleBars(byAccount)} valueClassName="privacy-mask" />
          </Card>
          <Card>
            <h3 className="mb-2 text-sm font-semibold text-on-surface">Остатки по счетам</h3>
            <div className="space-y-1 text-sm">
              {finances.accounts.map((a) => (
                <div key={a.bank} className="flex items-center justify-between gap-2">
                  <span className="truncate text-on-surface-variant">{a.bank}</span>
                  <span className="privacy-mask shrink-0 tabular-nums text-on-surface">
                    {formatRub(toKopecks(a.balance))}
                  </span>
                  <span className="w-24 shrink-0 text-right text-xs text-on-surface-variant">{a.updated}</span>
                </div>
              ))}
            </div>
          </Card>
        </div>
      </div>

      <Card>
        <h3 className="mb-2 text-sm font-semibold text-on-surface">Последние записи</h3>
        <div className="space-y-1 text-sm">
          {shown
            .slice()
            .sort((a, b) => b.date.localeCompare(a.date))
            .slice(0, 50)
            .map((t) => (
              <div key={t.id} className="flex items-center gap-2 border-b border-outline-variant py-1 last:border-0">
                <span className="w-24 shrink-0 tabular-nums text-on-surface-variant">{t.date}</span>
                <span className="w-28 shrink-0 truncate text-on-surface-variant">{t.category ?? '—'}</span>
                <span className="flex-1 truncate text-on-surface-variant" title={t.description ?? t.place ?? ''}>
                  {t.place ?? t.description ?? ''}
                </span>
                <span className="w-24 shrink-0 truncate text-xs text-on-surface-variant">{t.account ?? ''}</span>
                <span
                  className={`privacy-mask w-28 shrink-0 text-right tabular-nums ${
                    t.kind === 'income' ? 'text-secondary' : 'text-on-surface'
                  }`}
                >
                  {t.kind === 'income' ? '+' : ''}
                  {formatRub(toKopecks(t.amount))}
                </span>
              </div>
            ))}
        </div>
      </Card>
      </div>
    </Section>
  )
}
