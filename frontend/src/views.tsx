import { useMemo, useState } from 'react'
import { api } from './api'
import type { Audits, DuplicateGroup, Finances, Finding, NamedTotal, Stats } from './api'
import { useResource } from './hooks/useResource'
import { PeriodBars, ShareBars } from './FinanceCharts'
import type { Share } from './FinanceCharts'
import { dayBars, monthBars } from './financeSeries'
import { Badge, BarList, Card, ErrorBox, Label, Ring, Section, Spinner, Stat } from './components/ui'
import { formatRub, monthLabel, monthOf, toKopecks } from './money'

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

export function SettingsView({ stats }: { stats: Stats }) {
  // Changelog не критичен для этого вида: без него просто нет версии и трёх
  // последних релизов, поэтому падение запроса рендерится как отсутствие
  // данных, а не как ошибка страницы.
  const res = useResource(api.changelog)
  const log = res.status === 'ready' ? res.data : null

  const boxes = Object.entries(stats.by_category).sort((a, b) => b[1] - a[1])
  const empty = boxes.filter(([, n]) => n === 0).length
  const latest = (log?.releases ?? []).slice(0, 3)

  return (
    <div className="space-y-6">
      <header>
        <Label className="text-secondary">Визуализация и кастомизация</Label>
        <h1 className="mt-1 text-4xl">Настройки базы.</h1>
        <p className="mt-2 text-sm text-on-surface-variant">
          Каталог знаний как физический артефакт. Каждый ящик — категория.
        </p>
      </header>

      <div className="flex flex-col gap-8 xl:flex-row">
        {/* Ящики: структура — прямые углы, стопка с волосяными разделителями. */}
        <div className="min-w-0 flex-1 divide-y divide-outline-variant border border-outline-variant bg-surface-low">
          {boxes.map(([cat, n]) => (
            <div key={cat} className="flex items-center justify-between gap-3 px-5 py-4">
              <span className="truncate text-sm" title={cat}>
                {cat}
              </span>
              <span className="shrink-0 rounded-full bg-secondary px-2.5 py-0.5 font-mono text-xs font-bold text-white tabular-nums">
                {n}
              </span>
            </div>
          ))}
        </div>

        <aside className="shrink-0 space-y-4 xl:w-96">
          <Card className="border-l-2 border-l-secondary">
            <h2 className="text-xl">Информация о базе</h2>
            <dl className="mt-3 divide-y divide-outline-variant text-sm">
              {(
                [
                  ['Записей', String(stats.total)],
                  ['Категорий', String(boxes.length)],
                  ['Версия каталога', log?.current_version ? `v${log.current_version} · ${log.current_date ?? '—'}` : '—'],
                ] as const
              ).map(([k, v]) => (
                <div key={k} className="flex items-center justify-between py-2">
                  <dt className="label">{k}</dt>
                  <dd className="font-mono text-xs tabular-nums text-secondary">{v}</dd>
                </div>
              ))}
            </dl>
            {log?.current_tagline && (
              <p className="mt-2 text-xs italic text-on-surface-variant">{log.current_tagline}</p>
            )}
          </Card>

          <div className="grid grid-cols-2 gap-4">
            <Stat label="Активные ящики" value={boxes.length - empty} />
            <Stat label="Пустые ящики" value={empty} tone="muted" />
          </div>

          {latest.length > 0 && (
            <Card>
              <Label>Что нового</Label>
              <div className="mt-3 space-y-4">
                {latest.map((r, i) => (
                  <div key={r.version} className="space-y-1.5">
                    <div className="flex items-center gap-2">
                      <span className="font-headline text-base font-bold">v{r.version}</span>
                      {i === 0 && (
                        <span className="rounded bg-secondary px-1.5 py-0.5 font-label text-[9px] font-bold uppercase text-white">
                          latest
                        </span>
                      )}
                      <span className="ml-auto label">{r.date ?? ''}</span>
                    </div>
                    {r.tagline && <p className="text-xs italic text-on-surface-variant">{r.tagline}</p>}
                    {Object.entries(r.sections).map(([name, items]) => (
                      <div key={name}>
                        <span className="label text-secondary">{name}</span>
                        <ul className="mt-1 list-disc space-y-1 pl-4 text-xs text-on-surface-variant">
                          {items.slice(0, 3).map((it, j) => (
                            <li key={j}>{it.length > 160 ? `${it.slice(0, 160)}…` : it}</li>
                          ))}
                        </ul>
                      </div>
                    ))}
                  </div>
                ))}
              </div>
            </Card>
          )}
        </aside>
      </div>
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

/** NamedTotal с провода → строка списка долей. */
function toShare(t: NamedTotal): Share {
  return { name: t.name, kopecks: toKopecks(t.total) }
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

  // Арифметику считает сервер. Период уходит туда же ключом ресурса: смена
  // месяца — это другой запрос, а не пересчёт готовой сводки здесь, иначе
  // рядом с серверной реализацией появилась бы вторая, обязанная совпадать.
  const summaryRes = useResource(() => api.financeSummary(month === '' ? [] : [month]), { key: month })
  const summary = summaryRes.status === 'ready' ? summaryRes.data : null

  // Сегодня — параметром, а не внутри подготовки рядов: так ряды остаются
  // чистыми функциями и проверяются без подмены часов.
  const today = useMemo(() => new Date().toISOString().slice(0, 10), [])

  const shown = useMemo(
    () => (month === '' ? finances.transactions : finances.transactions.filter((t) => monthOf(t.date) === month)),
    [finances.transactions, month],
  )

  const balance = finances.accounts.reduce((n, a) => n + toKopecks(a.balance), 0)
  const spent = summary ? toKopecks(summary.expenses) : 0
  const earned = summary ? toKopecks(summary.income) : 0
  const top = summary?.byCategory[0]

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
        <Stat
          label={`Расходы (${summary?.expenseCount ?? 0})`}
          value={<span className="privacy-mask">{formatRub(spent)}</span>}
        />
        <Stat
          label={`Доходы (${summary?.incomeCount ?? 0})`}
          value={<span className="privacy-mask">{formatRub(earned)}</span>}
        />
        <Stat label="Разница" value={<span className="privacy-mask">{formatRub(earned - spent)}</span>} />
      </div>

      {summaryRes.status === 'failed' && <ErrorBox message={summaryRes.error} />}
      {summaryRes.status === 'loading' && <Spinner />}

      {summary && (
        <>
          {/* Bento категорий: доля каждой в расходах периода. */}
          {summary.byCategory.length > 0 && (
            <Card>
              <h3 className="mb-2 text-sm font-semibold text-on-surface">Распределение по категориям</h3>
              <div className="grid grid-cols-2 gap-px bg-outline-variant sm:grid-cols-4 xl:grid-cols-6">
                {summary.byCategory.map((c) => (
                  <div key={c.name} className="bg-surface-low">
                    <Ring
                      percent={spent > 0 ? Math.round((toKopecks(c.total) / spent) * 100) : 0}
                      label={c.name}
                    />
                  </div>
                ))}
              </div>
            </Card>
          )}

          {top && (
            <Card>
              <div className="text-sm text-on-surface-variant">Топ категория</div>
              <div className="mt-1 flex items-baseline gap-2">
                <span className="text-2xl font-bold text-on-surface">{top.name}</span>
                <span className="privacy-mask tabular-nums text-on-surface-variant">
                  {formatRub(toKopecks(top.total))}
                </span>
                <span className="text-sm text-on-surface-variant">
                  {/* Доля от отрицательной суммы — не доля ни от чего: когда
                      возвраты перевешивают покупки, процент меняет знак и
                      читается как бессмыслица. */}
                  {spent <= 0 ? '—' : `${Math.round((toKopecks(top.total) / spent) * 100)}% расходов`}
                </span>
              </div>
            </Card>
          )}

          <div className="grid gap-4 xl:grid-cols-3">
            <div className="space-y-4 xl:col-span-2">
              <Card>
                <h3 className="mb-3 text-sm font-semibold text-on-surface">Помесячная динамика</h3>
                <PeriodBars bars={monthBars(summary.byMonth, today)} />
              </Card>
              <Card>
                <h3 className="mb-3 text-sm font-semibold text-on-surface">Плотность транзакций (31 день)</h3>
                <PeriodBars bars={dayBars(summary.byDay, today)} height="h-36" />
              </Card>
              <Card>
                <h3 className="mb-3 text-sm font-semibold text-on-surface">Детализация подкатегорий</h3>
                <ShareBars
                  items={summary.bySubcategory.map((s) => ({
                    // Стрелка живёт здесь: на проводе категория и подкатегория
                    // приходят разными полями, потому что это подпись, а не данные.
                    name: `${s.category} → ${s.subcategory}`,
                    kopecks: toKopecks(s.total),
                  }))}
                />
              </Card>
            </div>

            <div className="space-y-4">
              <Card>
                <h3 className="mb-3 text-sm font-semibold text-on-surface">Потоки доходов</h3>
                <ShareBars items={summary.incomeBySource.map(toShare)} limit={10} />
              </Card>
              <Card>
                <h3 className="mb-3 text-sm font-semibold text-on-surface">Топ мест</h3>
                <ShareBars items={summary.byPlace.map(toShare)} limit={12} />
              </Card>
              <Card>
                <h3 className="mb-3 text-sm font-semibold text-on-surface">Источники оплаты</h3>
                <ShareBars items={summary.bySource.map(toShare)} limit={8} />
              </Card>
              <Card>
                <h3 className="mb-3 text-sm font-semibold text-on-surface">Расходы по счетам</h3>
                <ShareBars items={summary.byAccount.map(toShare)} limit={8} />
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
        </>
      )}

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
