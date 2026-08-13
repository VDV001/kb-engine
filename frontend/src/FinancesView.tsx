import { useMemo, useState } from 'react'
import { AccountsCard } from './AccountsCard'
import { api } from './api'
import type { Finances, Transaction } from './api'
import { useResource } from './hooks/useResource'
import { Icon } from './components/Icon'
import type { IconName } from './components/icons'
import { CollapsibleSection, SectionHead } from './components/SectionHead'
import { ErrorBox, Spinner } from './components/ui'
import { filterJournal, sortJournal } from './financeJournal'
import { dayBars, monthBars } from './financeSeries'
import { formatRub, monthLabel, monthOf, toKopecks, todayLocal } from './money'

// Вид «Финансовый архив», перенесённый из Python-дашборда поблочно: те же
// секции в том же порядке и с той же типографикой. Арифметику считает сервер
// (GET /api/finances/summary), здесь остаются показ и журнал — он листает и
// фильтрует сами строки, поэтому берёт их из /api/finances.

const PAGE_SIZE = 20

const CATEGORY_ICONS: Record<string, IconName> = {
  Еда: 'restaurant',
  Транспорт: 'directions_car',
  Жильё: 'home',
  Подписки: 'subscriptions',
  Развлечения: 'sports_esports',
  Здоровье: 'health_and_safety',
  Одежда: 'checkroom',
  Прочее: 'more_horiz',
  Образование: 'school',
  Связь: 'mobile',
  Красота: 'spa',
  Спорт: 'fitness_center',
}

// Иконки счетов, с которых платят. Раньше карточка показывала не счета, а
// поле source: у дохода это «Зарплата» или «Перевод от мамы», а у расхода —
// способ, которым запись попала в книгу («Чек», «Вручную»). Под заголовком
// «Источники оплаты» это читалось как мусор, потому что мусором и было:
// два несовместимых смысла в одном поле.
const ACCOUNT_ICONS: Record<string, IconName> = {
  Наличные: 'payments',
  Карта: 'credit_card',
}

function pct(part: number, whole: number): number {
  return whole > 0 ? Math.round((part / whole) * 100) : 0
}

/** Столбчатый график периода: помесячная динамика и плотность по дням. */
function Bars({
  bars,
  height,
}: {
  bars: { key: string; label: string; kopecks: number; current?: boolean }[]
  height: string
}) {
  if (bars.length === 0) {
    return <p className="label py-8 text-center opacity-40">Нет данных</p>
  }
  const max = Math.max(1, ...bars.map((b) => b.kopecks))
  return (
    <>
      <div className={`flex items-end justify-between gap-[3px] ${height}`}>
        {bars.map((b) => (
          <div key={b.key} className="group relative flex h-full min-w-0 flex-1 flex-col justify-end">
            <span className="privacy-mask tabular pointer-events-none absolute -top-6 left-1/2 z-10 hidden -translate-x-1/2 whitespace-nowrap rounded bg-primary-container px-1.5 py-0.5 text-[10px] text-on-primary group-hover:block">
              {formatRub(b.kopecks)}
            </span>
            <div
              className={`w-full rounded-t ${
                b.current ? 'bg-secondary' : b.kopecks > 0 ? 'bg-donut-primary opacity-70' : 'bg-surface-highest opacity-30'
              }`}
              // Минимум 2%: иначе период без трат выглядит отсутствующим столбцом,
              // и ряд читается короче, чем он есть.
              style={{ height: `${Math.max((b.kopecks / max) * 100, 2)}%` }}
            />
          </div>
        ))}
      </div>
      <div className="label mt-3 flex justify-between text-[9px] opacity-30">
        <span>{bars[0].label}</span>
        <span>{bars[bars.length - 1].label}</span>
      </div>
    </>
  )
}

/**
 * Пустой экран объясняет, ПОЧЕМУ он пуст, а причин четыре, не одна.
 *
 * `finances === null` означает «спросили и не смогли»: запрос упал, и до
 * причины отсюда не дотянуться — в тексте отказа лежит путь к личному файлу,
 * поэтому сервер отдаёт клиенту только факт, а причину пишет в свой терминал.
 * Прежде этот случай подставлял пустоту и печатал совет добавить флаги,
 * которые уже переданы: отказ был переодет в законное состояние.
 *
 * Подключён ли журнал, знает движок, а не страница: спрашиваем `/api/engine`.
 * `null` — «ответа ещё нет либо сборка о своих источниках не сообщает», и на
 * нём остаётся прежний текст: выдуманная причина хуже неназванной.
 */
export function FinancesView({ finances, masked }: { finances: Finances | null; masked: boolean }) {
  const eng = useResource(api.engine)
  const ledgerConnected: boolean | null =
    eng.status === 'ready'
      ? (eng.data.sources?.find((s) => s.flag === 'ledger')?.connected ?? null)
      : null

  if (finances === null) {
    return (
      <EmptyFinances
        text="Журнал не прочитан — движок ответил отказом. Причина названа в терминале, где висит serve."
      />
    )
  }
  if (finances.transactions.length === 0 && finances.accounts.length === 0) {
    return (
      <EmptyFinances
        text={
          ledgerConnected === true
            ? 'Журнал подключён и пока пуст — ни одной записи в нём нет.'
            : 'Леджер не подключён — запустите serve с флагами --ledger и --from.'
        }
      />
    )
  }
  return <FinancesBody finances={finances} masked={masked} />
}

function EmptyFinances({ text }: { text: string }) {
  return (
    <div className="p-12 text-center">
      <Icon name="receipt_long" className="mb-4 text-5xl opacity-10" />
      <p className="label opacity-60">{text}</p>
    </div>
  )
}

function FinancesBody({ finances, masked }: { finances: Finances; masked: boolean }) {
  // Несколько месяцев сразу — как в исходном дашборде. Пустой набор значит
  // «за всё время».
  const [selected, setSelected] = useState<string[]>([])
  const [showFilters, setShowFilters] = useState(false)
  const [filterCat, setFilterCat] = useState('all')
  const [dateFrom, setDateFrom] = useState('')
  const [dateTo, setDateTo] = useState('')
  const [search, setSearch] = useState('')
  const [sortField, setSortField] = useState<'date' | 'amount'>('date')
  const [sortDir, setSortDir] = useState<'asc' | 'desc'>('desc')
  const [page, setPage] = useState(1)

  const monthsKey = useMemo(() => [...selected].sort().join(','), [selected])
  const summaryRes = useResource(() => api.financeSummary([...selected].sort()), { key: monthsKey })
  const summary = summaryRes.status === 'ready' ? summaryRes.data : null

  const today = useMemo(() => todayLocal(), [])

  const allMonths = useMemo(
    () => Array.from(new Set(finances.transactions.map((t) => monthOf(t.date)))).sort().reverse(),
    [finances.transactions],
  )

  // Ряд последней недели считается один раз, а не на каждый столбец.
  const week = useMemo(() => {
    const bars = summary ? dayBars(summary.byDay, today, 7) : []
    return { bars, max: Math.max(1, ...bars.map((b) => b.kopecks)) }
  }, [summary, today])

  const spent = summary ? toKopecks(summary.expenses) : 0
  const earned = summary ? toKopecks(summary.income) : 0
  const periodText = selected.length === 0 ? 'За всё время' : [...selected].sort().map(monthLabel).join(' · ')

  // Место → категория, чтобы подобрать иконку. Это не пересчёт сумм, а поиск
  // подписи: суммы по местам уже посчитаны сервером.
  const placeCategory = useMemo(() => {
    const m = new Map<string, string>()
    for (const t of finances.transactions) {
      if (t.place && t.category && !m.has(t.place)) m.set(t.place, t.category)
    }
    return m
  }, [finances.transactions])

  // Подкатегории без привязки к категории — перегруппировка уже готовых строк,
  // а не вторая реализация арифметики: сервер прислал суммы, здесь они только
  // складываются по имени.
  const subcatCloud = useMemo(() => {
    if (!summary) return []
    const m = new Map<string, number>()
    for (const s of summary.bySubcategory) {
      m.set(s.subcategory, (m.get(s.subcategory) ?? 0) + toKopecks(s.total))
    }
    return [...m.entries()].sort((a, b) => b[1] - a[1])
  }, [summary])

  // Журнал листает сами строки, поэтому фильтрует их сам — но отбор и порядок
  // живут в проверенном модуле, а не здесь.
  const journal = useMemo(
    () =>
      sortJournal(
        filterJournal(finances.transactions, {
          months: selected,
          category: filterCat === 'all' ? '' : filterCat,
          from: dateFrom,
          to: dateTo,
          search,
        }),
        sortField,
        sortDir,
      ),
    [finances.transactions, selected, filterCat, dateFrom, dateTo, search, sortField, sortDir],
  )

  const pages = Math.max(1, Math.ceil(journal.length / PAGE_SIZE))
  const current = Math.min(page, pages)
  const slice = journal.slice((current - 1) * PAGE_SIZE, current * PAGE_SIZE)

  const categories = useMemo(
    () => Array.from(new Set(finances.transactions.map((t) => t.category ?? '').filter(Boolean))).sort(),
    [finances.transactions],
  )

  function toggleMonth(m: string) {
    setPage(1)
    setSelected((prev) => (prev.includes(m) ? prev.filter((x) => x !== m) : [...prev, m]))
  }

  function sortBy(field: 'date' | 'amount') {
    if (sortField === field) {
      setSortDir((d) => (d === 'asc' ? 'desc' : 'asc'))
    } else {
      setSortField(field)
      setSortDir('desc')
    }
  }

  // Экспорт делает сервер: excelize у движка уже есть, а собирать zip с XML
  // в браузере ради того же результата — лишняя библиотека. Строки уходят
  // ровно те, что на экране, поэтому фильтрация остаётся в одном месте.
  async function exportXLSX() {
    const blob = await api.financeExport(
      journal.map((t) => ({
        date: t.date,
        kind: t.kind,
        category: t.category ?? '',
        subcategory: t.subcategory ?? '',
        place: t.place ?? '',
        description: t.description ?? '',
        amount: t.amount,
        account: t.account ?? '',
      })),
    )
    const a = document.createElement('a')
    a.href = URL.createObjectURL(blob)
    a.download = 'finances.xlsx'
    a.click()
    URL.revokeObjectURL(a.href)
  }

  return (
    <div className={masked ? 'privacy-on' : undefined}>
      {/* Карточка счетов идёт колонкой слева и не уезжает при прокрутке: она
          отвечает на вопрос «где деньги», который задают в любом месте
          страницы. На узком экране колонка схлопывается и карточка встаёт
          сверху — там же, где она была в мобильном виде старого дашборда. */}
      <div className="flex flex-col gap-8 xl:flex-row">
        <aside className="shrink-0 xl:w-72">
          <div className="xl:sticky xl:top-24">
            <AccountsCard
              transfersExcluded={summary?.excludedTransferCount ?? 0}
              accounts={finances.accounts}
              expenses={summary?.expenses ?? '0'}
              income={summary?.income ?? '0'}
              today={today}
            />
          </div>
        </aside>
        <div className="min-w-0 flex-1">
      {/* ===== Шапка ===== */}
      <section className="mb-16">
        <div className="mb-12 flex flex-col justify-between gap-6 md:flex-row md:items-baseline">
          <div className="max-w-xl">
            <p className="label mb-3 tracking-[0.3em] text-secondary">{periodText}</p>
            <h1 className="mb-4 text-4xl leading-none tracking-tight md:text-5xl">Финансовый Архив.</h1>
            <p className="leading-relaxed text-on-surface-variant">
              Структурированный учёт расходов и доходов. Данные из леджера, перечитываются на каждый запрос.
            </p>
          </div>
          {/* Итог по счетам переехал в карточку слева и здесь больше не
              повторяется: одно и то же число дважды на одном экране заставляет
              искать между ними разницу, которой нет. */}
          <div className="flex flex-col items-end">
            <div className="flex gap-2">
              <span className="label bg-surface-high px-2 py-1 text-[10px]">
                {summary?.expenseCount ?? 0} записей
              </span>
              <span className="label bg-surface-high px-2 py-1 text-[10px]">
                {summary?.byCategory.length ?? 0} категорий
              </span>
            </div>
          </div>
        </div>

        {/* ===== Выбор периода ===== */}
        <div className="mb-6 overflow-hidden rounded-lg bg-surface-low">
          <div className="flex items-center gap-3 px-6 pb-3 pt-5">
            <Icon name="date_range" className="text-base opacity-30" />
            <span className="label text-[10px] opacity-40">Период</span>
            <div className="mx-4 h-px flex-1 bg-outline-variant opacity-30" />
            <span className="label text-[10px] text-secondary">{periodText}</span>
          </div>
          <div className="flex flex-wrap items-center gap-2 px-5 pb-5">
            <button
              type="button"
              onClick={() => {
                setSelected([])
                setPage(1)
              }}
              className={`label px-3 py-1.5 text-[10px] transition-colors ${
                selected.length === 0
                  ? 'bg-secondary text-white'
                  : 'bg-surface-high text-on-surface-variant hover:bg-surface-highest'
              }`}
            >
              За всё время
            </button>
            {allMonths.map((m) => (
              <button
                key={m}
                type="button"
                onClick={() => toggleMonth(m)}
                className={`label px-3 py-1.5 text-[10px] transition-colors ${
                  selected.includes(m)
                    ? 'bg-secondary text-white'
                    : 'bg-surface-high text-on-surface-variant hover:bg-surface-highest'
                }`}
              >
                {monthLabel(m)}
              </button>
            ))}
          </div>
        </div>

        {summaryRes.status === 'failed' && <ErrorBox message={summaryRes.error} />}
        {summaryRes.status === 'loading' && <Spinner />}

        {summary && (
          <div className="grid grid-cols-1 gap-4 md:grid-cols-4">
            <div className="col-span-1 flex min-h-[200px] flex-col justify-between rounded-lg bg-surface-low p-8 md:col-span-2">
              <div>
                <span className="label text-on-surface-variant">Общие расходы</span>
                <div className="mt-6 flex h-12 items-end gap-1">
                  {week.bars.map((b) => (
                    <div
                      key={b.key}
                      title={b.label}
                      className="flex-1 rounded-t bg-secondary opacity-60"
                      style={{ height: `${Math.max((b.kopecks / week.max) * 100, 4)}%` }}
                    />
                  ))}
                </div>
              </div>
              <div className="mt-4 flex items-end justify-between">
                <span className="privacy-mask text-3xl font-bold">{formatRub(spent)}</span>
                <span className="label text-[10px] text-secondary">{periodText}</span>
              </div>
              {/* Исключённое называется рядом с числом, которое оно объясняет:
                  сумма строк в журнале не сходится с расходами ровно на эту
                  величину, и без строки расхождение выглядит ошибкой. Молчит,
                  когда исключать было нечего. */}
              {summary.excludedTransferCount > 0 && (
                <p className="label mt-2 text-[10px] opacity-60">
                  переводы себе не в счёт:{' '}
                  <span className="privacy-mask">{formatRub(toKopecks(summary.excludedTransfers))}</span>{' '}
                  ({summary.excludedTransferCount})
                </p>
              )}
            </div>

            <div className="flex min-h-[200px] flex-col justify-between rounded-lg bg-surface-high p-8">
              <div className="flex items-center justify-between">
                <span className="label text-on-surface-variant">Доходы</span>
                <span className="label text-[9px] opacity-40">{summary.incomeCount} шт</span>
              </div>
              <div>
                <span className="privacy-mask text-3xl font-bold">{formatRub(earned)}</span>
                <div className="mt-3 space-y-1.5">
                  {summary.incomeBySource.slice(0, 3).map((s) => (
                    <div key={s.name} className="flex items-center justify-between text-xs">
                      <span className="truncate text-on-surface-variant">{s.name}</span>
                      <span className="privacy-mask tabular ml-2 shrink-0">{formatRub(toKopecks(s.total))}</span>
                    </div>
                  ))}
                </div>
              </div>
            </div>

            <div className="flex min-h-[200px] flex-col justify-between rounded-lg bg-primary-container p-8 text-on-primary shadow-xl">
              <span className="label opacity-60">Топ категория</span>
              <div>
                <span className="privacy-mask text-2xl font-bold">
                  {summary.byCategory[0] ? formatRub(toKopecks(summary.byCategory[0].total)) : '—'}
                </span>
                <p className="label mt-1 text-sm opacity-80">{summary.byCategory[0]?.name ?? '—'}</p>
              </div>
              <div className="flex items-center gap-2">
                <Icon name="trending_up" className="text-base" />
                <span className="label text-[10px]">
                  {summary.byCategory[0] ? `${pct(toKopecks(summary.byCategory[0].total), spent)}% расходов` : '—'}
                </span>
              </div>
            </div>
          </div>
        )}
      </section>

      {summary && (
        <>
          {/* ===== Категории ===== */}
          <section className="mb-16">
            <SectionHead title="Распределение по категориям" />
            <div className="grid grid-cols-2 gap-px bg-outline-variant p-px md:grid-cols-4 lg:grid-cols-8">
              {summary.byCategory.map((c) => {
                const p = pct(toKopecks(c.total), spent)
                return (
                  <div key={c.name} className="flex min-h-[140px] flex-col items-center justify-center bg-bg p-6">
                    <div className="relative mb-4 h-16 w-16">
                      <svg viewBox="0 0 36 36" className="h-full w-full -rotate-90">
                        <circle cx="18" cy="18" r="16" fill="none" strokeWidth="2" className="stroke-surface-highest" />
                        <circle
                          cx="18"
                          cy="18"
                          r="16"
                          fill="none"
                          strokeWidth="2"
                          className="stroke-secondary"
                          strokeDasharray={`${p},100`}
                        />
                      </svg>
                    </div>
                    <span className="label w-full truncate text-center text-[10px] tracking-[0.15em]" title={c.name}>
                      {c.name}
                    </span>
                    <span className="privacy-mask mt-1 text-xs font-bold">{p}%</span>
                  </div>
                )
              })}
            </div>
          </section>

          {/* ===== Асимметричная раскладка 8/4 ===== */}
          <div className="mb-16 grid grid-cols-1 gap-8 lg:grid-cols-12">
            <div className="space-y-10 lg:col-span-8">
              <section>
                <SectionHead
                  title="Помесячная динамика"
                  right={`Макс: ${formatRub(Math.max(0, ...summary.byMonth.map((m) => toKopecks(m.total))))}`}
                />
                <div className="rounded-lg bg-surface-low p-8">
                  <Bars bars={monthBars(summary.byMonth, today)} height="h-52" />
                </div>
              </section>

              <section>
                <SectionHead title="Плотность транзакций (31 день)" />
                <div className="rounded-lg bg-surface-low p-6">
                  <Bars bars={dayBars(summary.byDay, today)} height="h-36" />
                </div>
              </section>

              <CollapsibleSection title="Детализация подкатегорий" count={summary.bySubcategory.length}>
                <div className="space-y-3">
                  {summary.bySubcategory.slice(0, 15).map((s) => {
                    const kop = toKopecks(s.total)
                    const max = toKopecks(summary.bySubcategory[0].total)
                    return (
                      <div key={`${s.category}/${s.subcategory}`} className="flex items-center gap-4">
                        <span
                          className="label w-48 shrink-0 truncate text-right text-[10px] opacity-60"
                          title={`${s.category} → ${s.subcategory}`}
                        >
                          {s.category} → {s.subcategory}
                        </span>
                        <div className="h-2 min-w-0 flex-1 overflow-hidden rounded-full bg-surface-high">
                          <div
                            className="h-full rounded-full bg-donut-primary"
                            style={{ width: `${Math.max(0, kop / Math.max(1, max)) * 100}%` }}
                          />
                        </div>
                        <span className="privacy-mask label w-24 shrink-0 text-right text-[10px]">
                          {formatRub(kop)}
                        </span>
                        <span className="label w-8 text-[9px] opacity-40">{pct(kop, spent)}%</span>
                      </div>
                    )
                  })}
                  {summary.bySubcategory.length === 0 && (
                    <p className="label opacity-40">Нет данных</p>
                  )}
                </div>
              </CollapsibleSection>
            </div>

            <div className="space-y-10 lg:col-span-4">
              <section>
                <SectionHead title="Потоки доходов" />
                <div className="space-y-4 rounded-lg bg-surface-low p-6">
                  {summary.incomeBySource.length === 0 && <p className="label opacity-40">Нет данных о доходах</p>}
                  {summary.incomeBySource.map((s, i) => {
                    const kop = toKopecks(s.total)
                    const max = toKopecks(summary.incomeBySource[0].total)
                    const colour = ['bg-secondary', 'bg-donut-primary', 'bg-outline', 'bg-on-surface-variant'][i % 4]
                    return (
                      <div key={s.name}>
                        <div className="mb-1.5 flex items-center justify-between">
                          <span className="label text-xs">{s.name}</span>
                          <span className="privacy-mask text-sm font-bold">{formatRub(kop)}</span>
                        </div>
                        <div className="flex items-center gap-2">
                          <div className="h-3 flex-1 overflow-hidden rounded-full bg-surface-highest">
                            <div className={`h-full rounded-full ${colour}`} style={{ width: `${pct(kop, max)}%` }} />
                          </div>
                          <span className="label w-8 text-right text-[9px] opacity-50">{pct(kop, earned)}%</span>
                        </div>
                      </div>
                    )
                  })}
                </div>
              </section>

              <CollapsibleSection title="Топ мест" count={summary.byPlace.length}>
                <div className="max-h-[420px] overflow-y-auto rounded-lg bg-surface-low">
                  {summary.byPlace.length === 0 && <p className="label p-6 opacity-40">Нет данных</p>}
                  {summary.byPlace.slice(0, 12).map((p, idx) => {
                    const kop = toKopecks(p.total)
                    const max = toKopecks(summary.byPlace[0].total)
                    const icon = CATEGORY_ICONS[placeCategory.get(p.name) ?? ''] ?? 'receipt_long'
                    return (
                      <div key={p.name} className="flex items-center gap-3 border-b border-outline-variant px-5 py-3">
                        <span className="label w-5 text-xs opacity-20">{idx + 1}</span>
                        <Icon name={icon} className="text-base text-secondary opacity-60" />
                        <div className="min-w-0 flex-1">
                          <div className="flex items-center justify-between">
                            <span className="truncate text-sm font-semibold">{p.name}</span>
                            <span className="privacy-mask ml-2 shrink-0 text-sm font-bold">{formatRub(kop)}</span>
                          </div>
                          <div className="mt-1 flex items-center gap-2">
                            <div className="h-1 flex-1 overflow-hidden rounded-full bg-surface-highest">
                              <div
                                className="h-full rounded-full bg-secondary opacity-50"
                                style={{ width: `${pct(kop, max)}%` }}
                              />
                            </div>
                            <span className="label text-[8px] opacity-30">{p.count}x</span>
                          </div>
                        </div>
                      </div>
                    )
                  })}
                </div>
              </CollapsibleSection>

              <CollapsibleSection title="Подкатегории" count={subcatCloud.length} defaultOpen={false}>
                <div className="flex flex-wrap gap-2">
                  {subcatCloud.length === 0 && <p className="label opacity-40">Нет данных</p>}
                  {subcatCloud.map(([name, kop]) => {
                    const share = kop / Math.max(1, subcatCloud[0][1])
                    return (
                      <span
                        key={name}
                        title={formatRub(kop)}
                        className="label border border-outline-variant bg-surface-low px-3 py-1.5"
                        style={{ opacity: 0.4 + Math.min(share, 1) * 0.6, fontSize: `${0.7 + Math.min(share, 1) * 0.5}rem` }}
                      >
                        {name}
                      </span>
                    )
                  })}
                </div>
              </CollapsibleSection>

              <CollapsibleSection title="Счета" count={summary.byAccount.length}>
                <div className="space-y-3 rounded-lg bg-surface-low p-6">
                  {summary.byAccount.length === 0 && <p className="label opacity-40">Нет данных</p>}
                  {summary.byAccount.map((s) => {
                    const kop = toKopecks(s.total)
                    return (
                      <div key={s.name} className="flex items-center gap-3">
                        <Icon name={ACCOUNT_ICONS[s.name] ?? 'account_balance_wallet'} className="text-base opacity-40" />
                        <div className="flex-1">
                          <div className="flex items-center justify-between">
                            <span className="label text-xs">{s.name}</span>
                            <span className="privacy-mask text-sm font-bold">{formatRub(kop)}</span>
                          </div>
                          <div className="mt-1 flex items-center gap-2">
                            <div className="h-1.5 flex-1 overflow-hidden rounded-full bg-surface-highest">
                              <div
                                className="h-full rounded-full bg-donut-primary opacity-50"
                                style={{ width: `${pct(kop, spent)}%` }}
                              />
                            </div>
                            <span className="label text-[8px] opacity-30">{pct(kop, spent)}%</span>
                          </div>
                        </div>
                      </div>
                    )
                  })}
                </div>
              </CollapsibleSection>
            </div>
          </div>
        </>
      )}

      {/* ===== Журнал ===== */}
      <section>
        <div className="mb-6 flex flex-col items-start justify-between gap-4 md:flex-row md:items-center">
          <div>
            <h3 className="text-2xl tracking-tight">Журнал записей</h3>
            <p className="label mt-1 text-[10px] tracking-[0.2em] text-on-surface-variant">Все транзакции</p>
          </div>
          <div className="flex gap-3">
            <button
              type="button"
              onClick={() => setShowFilters((v) => !v)}
              className="label flex items-center gap-2 bg-surface-high px-4 py-2 text-xs"
            >
              <Icon name={showFilters ? 'filter_list_off' : 'filter_list'} className="text-sm" /> Фильтры
            </button>
            <button
              type="button"
              onClick={exportXLSX}
              className="label flex items-center gap-2 bg-primary px-4 py-2 text-xs text-on-primary"
            >
              <Icon name="download" className="text-sm" /> Экспорт
            </button>
          </div>
        </div>

        {showFilters && (
          <div className="mb-6 rounded-lg bg-surface-low p-4">
            <div className="mb-3">
              <span className="label mb-2 block text-[10px] opacity-60">Категория</span>
              <div className="flex flex-wrap gap-2">
                {['all', ...categories].map((c) => (
                  <button
                    key={c}
                    type="button"
                    onClick={() => {
                      setFilterCat(c)
                      setPage(1)
                    }}
                    className={`label rounded-full px-3 py-1 text-xs ${
                      filterCat === c ? 'bg-secondary text-white' : 'bg-surface-high text-on-surface-variant'
                    }`}
                  >
                    {c === 'all' ? 'Все' : c}
                  </button>
                ))}
              </div>
            </div>
            <div className="mt-3 flex flex-wrap items-center gap-4">
              <label className="block">
                <span className="label mb-1 block text-[10px] opacity-60">Дата от</span>
                <input
                  type="date"
                  value={dateFrom}
                  onChange={(e) => {
                    setDateFrom(e.target.value)
                    setPage(1)
                  }}
                  className="label rounded border border-outline-variant bg-surface px-3 py-1.5 text-xs"
                />
              </label>
              <label className="block">
                <span className="label mb-1 block text-[10px] opacity-60">Дата до</span>
                <input
                  type="date"
                  value={dateTo}
                  onChange={(e) => {
                    setDateTo(e.target.value)
                    setPage(1)
                  }}
                  className="label rounded border border-outline-variant bg-surface px-3 py-1.5 text-xs"
                />
              </label>
              <label className="block">
                <span className="label mb-1 block text-[10px] opacity-60">Поиск</span>
                <input
                  type="text"
                  value={search}
                  placeholder="Место..."
                  onChange={(e) => {
                    setSearch(e.target.value)
                    setPage(1)
                  }}
                  className="label w-48 rounded border border-outline-variant bg-surface px-3 py-1.5 text-xs"
                />
              </label>
            </div>
          </div>
        )}

        <div className="overflow-x-auto">
          <table className="w-full border-collapse text-left">
            <thead className="bg-surface-low">
              <tr>
                <th
                  onClick={() => sortBy('date')}
                  className="label cursor-pointer px-4 py-4 text-[10px] font-medium text-on-surface-variant"
                >
                  Дата{' '}
                  <Icon
                    name={sortField === 'date' ? (sortDir === 'asc' ? 'keyboard_arrow_up' : 'keyboard_arrow_down') : 'unfold_more'}
                    className="text-xs"
                  />
                </th>
                <th className="label px-4 py-4 text-[10px] font-medium text-on-surface-variant">Категория</th>
                <th className="label hidden px-4 py-4 text-[10px] font-medium text-on-surface-variant md:table-cell">
                  Подкатегория
                </th>
                <th className="label px-4 py-4 text-[10px] font-medium text-on-surface-variant">Место</th>
                <th className="label hidden px-4 py-4 text-[10px] font-medium text-on-surface-variant lg:table-cell">
                  Описание
                </th>
                <th
                  onClick={() => sortBy('amount')}
                  className="label cursor-pointer px-4 py-4 text-right text-[10px] font-medium text-on-surface-variant"
                >
                  Сумма{' '}
                  <Icon
                    name={sortField === 'amount' ? (sortDir === 'asc' ? 'keyboard_arrow_up' : 'keyboard_arrow_down') : 'unfold_more'}
                    className="text-xs"
                  />
                </th>
                <th className="label hidden px-4 py-4 text-right text-[10px] font-medium text-on-surface-variant md:table-cell">
                  Источник
                </th>
              </tr>
            </thead>
            <tbody>
              {slice.map((t: Transaction) => (
                <tr key={t.id} className="border-b border-outline-variant">
                  <td className="label px-4 py-5 text-xs">{t.date}</td>
                  <td className="px-4 py-5 text-xs font-semibold">{t.category ?? ''}</td>
                  <td className="label hidden px-4 py-5 text-[10px] text-on-surface-variant md:table-cell">
                    {t.subcategory ?? ''}
                  </td>
                  <td className="px-4 py-5 text-xs">{t.place ?? ''}</td>
                  <td className="hidden px-4 py-5 text-xs italic opacity-60 lg:table-cell">{t.description ?? ''}</td>
                  <td className="privacy-mask px-4 py-5 text-right text-sm font-bold">
                    {t.kind === 'income' ? '+' : ''}
                    {formatRub(toKopecks(t.amount))}
                  </td>
                  <td className="label hidden px-4 py-5 text-right text-[10px] text-secondary md:table-cell">
                    {(t.source ?? '').toUpperCase()}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>

        {journal.length === 0 ? (
          <div className="mt-8 border-t-4 border-primary bg-surface-highest p-8 text-center">
            <Icon name="search_off" className="mb-4 text-5xl opacity-10" />
            <p className="label opacity-60">Транзакций не найдено.</p>
          </div>
        ) : (
          <div className="mt-6 flex items-center justify-between">
            <span className="label text-[10px] opacity-40">
              Показано {(current - 1) * PAGE_SIZE + 1}–{Math.min(current * PAGE_SIZE, journal.length)} из{' '}
              {journal.length}
            </span>
            {pages > 1 && (
              <div className="flex items-center gap-1.5">
                <button
                  type="button"
                  disabled={current === 1}
                  onClick={() => setPage(current - 1)}
                  className="flex h-8 w-8 items-center justify-center rounded border border-outline-variant text-on-surface-variant disabled:opacity-30"
                >
                  <Icon name="chevron_left" className="text-base" />
                </button>
                <span className="label px-2 text-[10px] tabular-nums">
                  {current} / {pages}
                </span>
                <button
                  type="button"
                  disabled={current === pages}
                  onClick={() => setPage(current + 1)}
                  className="flex h-8 w-8 items-center justify-center rounded border border-outline-variant text-on-surface-variant disabled:opacity-30"
                >
                  <Icon name="chevron_right" className="text-base" />
                </button>
              </div>
            )}
          </div>
        )}
      </section>
        </div>
      </div>
    </div>
  )
}
