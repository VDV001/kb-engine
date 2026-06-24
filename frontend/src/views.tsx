import { useMemo, useState } from 'react'
import type { Analytics, AnalyticsConfig, Audits, DuplicateGroup, Entry, Finding, Stats } from './api'
import { Badge, BarList, Card, Section, Stat } from './components/ui'

export function OverviewView({ stats }: { stats: Stats }) {
  return (
    <div className="space-y-6">
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
        <Stat label="Всего записей" value={stats.total} />
        <Stat label="Категорий" value={Object.keys(stats.by_category).length} />
        <Stat label="Canonical" value={stats.by_lifecycle['canonical'] ?? 0} />
        <Stat label="Outdated" value={stats.by_lifecycle['outdated'] ?? 0} />
      </div>
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
          className="flex-1 rounded-lg border border-slate-300 px-3 py-1.5 text-sm dark:border-slate-600 dark:bg-slate-800"
        />
        <select
          value={category}
          onChange={(e) => setCategory(e.target.value)}
          className="rounded-lg border border-slate-300 px-3 py-1.5 text-sm dark:border-slate-600 dark:bg-slate-800"
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
          <thead className="border-b border-slate-200 text-slate-500 dark:border-slate-700">
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
              <tr key={e.id} className="border-b border-slate-100 dark:border-slate-700/50">
                <td className="p-2 tabular-nums text-slate-400">{e.id}</td>
                <td className="p-2">
                  {e.url ? (
                    <a href={e.url} target="_blank" rel="noreferrer" className="text-sky-600 hover:underline">
                      {e.title}
                    </a>
                  ) : (
                    e.title
                  )}
                </td>
                <td className="p-2 text-slate-500">{e.category}</td>
                <td className="p-2"><Badge value={e.lifecycle} /></td>
                <td className="p-2">{e.verdict ? <Badge value={e.verdict} /> : <span className="text-slate-300">—</span>}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </Card>
      {filtered.length > 300 && (
        <p className="text-xs text-slate-400">Показаны первые 300 — уточните фильтр.</p>
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
          <p className="text-sm text-slate-400">Нет кандидатов.</p>
        ) : (
          <ul className="space-y-1.5">
            {items.map((f) => (
              <li key={f.EntryID} className="flex flex-wrap items-center gap-2 text-sm">
                <span className="tabular-nums text-slate-400">#{f.EntryID}</span>
                <span className="flex-1 truncate" title={f.Title}>{f.Title}</span>
                {f.Reasons.map((r) => (
                  <span key={r} className="rounded bg-slate-100 px-1.5 py-0.5 text-xs text-slate-500 dark:bg-slate-700">
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
          <Stat label="Недель в окне" value={analytics.growth.length} />
          <Stat label="Пик/неделю" value={maxWeek} />
        </div>
      </Section>

      <Card>
        <h3 className="mb-3 text-sm font-semibold text-slate-700 dark:text-slate-200">
          Рост по неделям (по дате создания)
        </h3>
        <div className="flex items-end gap-1" style={{ height: 140 }}>
          {analytics.growth.map((w) => (
            <div key={w.week} className="flex flex-1 flex-col items-center justify-end gap-1">
              <span className="text-[10px] tabular-nums text-slate-400">{w.count}</span>
              <div
                className="w-full rounded-t bg-sky-500"
                style={{ height: `${(w.count / maxWeek) * 100}%`, minHeight: w.count > 0 ? 2 : 0 }}
                title={`${w.week}: ${w.count}`}
              />
              <span className="text-[10px] text-slate-400">{w.week}</span>
            </div>
          ))}
        </div>
      </Card>

      <Card>
        <h3 className="mb-3 text-sm font-semibold text-slate-700 dark:text-slate-200">Размеры категорий</h3>
        <BarList data={categoryData} />
      </Card>

      {quotes.length > 0 && (
        <Section title="Манифест" subtitle={`${quotes.length} тезисов`}>
          <div className="space-y-2">
            {quotes.map((q, i) => (
              <Card key={i}>
                <p className="text-sm italic text-slate-700 dark:text-slate-200">«{q.quote}»</p>
                <p className="mt-1 text-xs text-slate-400">
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
                  <span className="text-sm font-semibold text-slate-700 dark:text-slate-200">{p.name}</span>
                  {(p.clusters ?? []).map((c) => (
                    <Badge key={c} value={c} />
                  ))}
                </div>
                {p.desc && <p className="mt-1 text-xs text-slate-500">{p.desc}</p>}
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
                <p className="text-sm font-semibold text-slate-700 dark:text-slate-200">{c.title}</p>
                <p className="mt-1 text-xs text-slate-500">A: {c.a}</p>
                <p className="text-xs text-slate-500">B: {c.b}</p>
                {c.resolution && (
                  <p className="mt-1 text-xs text-sky-600 dark:text-sky-400">→ {c.resolution}</p>
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
                  <span className="text-sm text-slate-700 dark:text-slate-200">{g.topic}</span>
                </div>
              </Card>
            ))}
          </div>
        </Section>
      )}
    </div>
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
              <span className="tabular-nums text-slate-500">ids: {g.EntryIDs.join(', ')}</span>
            </div>
            <div className="mt-1 truncate text-xs text-slate-400" title={g.Key}>{g.Key}</div>
          </Card>
        ))}
      </div>
    </Section>
  )
}
