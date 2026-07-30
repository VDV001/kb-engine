import { useMemo, useState } from 'react'
import type { Entry } from './api'
import {
  categoryLabel,
  dateOf,
  emptyFilter,
  filterEntries,
  pageWindow,
  sortByDate,
  statusOf,
  statusStyle,
  type CatalogFilter,
} from './catalog'
import { Label } from './components/ui'

// Пятнадцать, как в исходном дашборде: столько строк помещается на экран
// ноутбука без прокрутки до пагинации.
const PAGE_SIZE = 15

// Tag pills cycle through the first three tag roles, the way the Python
// dashboard colours them: by position, not by meaning — the meaning is the
// text, the colour only keeps neighbours apart.
const tagTone = [
  'bg-tag-bg-1 text-tag-text-1',
  'bg-tag-bg-2 text-tag-text-2',
  'bg-tag-bg-3 text-tag-text-3',
]

function Status({ e }: { e: Entry }) {
  const s = statusOf(e)
  return (
    <span className="flex items-center gap-2 whitespace-nowrap">
      <span className="h-1.5 w-1.5 rounded-full" style={{ background: s.tone }} />
      <span className="label" style={{ color: s.tone }}>
        {s.label}
      </span>
    </span>
  )
}

function Tags({ tags }: { tags?: string[] }) {
  return (
    <span className="flex flex-wrap gap-1.5">
      {(tags ?? []).slice(0, 3).map((t, i) => (
        <span
          key={t}
          className={`rounded px-2 py-0.5 font-label text-[10px] font-bold uppercase ${tagTone[i]}`}
        >
          {t}
        </span>
      ))}
    </span>
  )
}

function Pagination({
  page,
  pages,
  onPage,
}: {
  page: number
  pages: number
  onPage: (p: number) => void
}) {
  if (pages <= 1) return null
  const arrow = 'flex h-8 w-8 items-center justify-center rounded border border-outline-variant text-on-surface-variant disabled:opacity-30'
  return (
    <div className="flex items-center gap-1.5">
      <button type="button" className={arrow} disabled={page === 1} onClick={() => onPage(page - 1)} aria-label="Назад">
        ‹
      </button>
      {pageWindow(page, pages).map((p, i) =>
        p === null ? (
          <span key={`dots-${i}`} className="px-1 select-none text-on-surface-variant">
            …
          </span>
        ) : (
          <button
            key={p}
            type="button"
            onClick={() => onPage(p)}
            className={`h-8 w-8 rounded font-label text-xs ${
              p === page ? 'bg-secondary font-bold text-white' : 'text-on-surface-variant'
            }`}
          >
            {p}
          </button>
        ),
      )}
      <button type="button" className={arrow} disabled={page === pages} onClick={() => onPage(page + 1)} aria-label="Вперёд">
        ›
      </button>
    </div>
  )
}

const selectClass =
  'rounded-md border border-outline-variant bg-surface-low px-3 py-1.5 text-sm text-on-surface'

/**
 * CatalogView is the full catalog browser, the view the KB dashboard calls
 * Archives: category sidebar with counts, four filters, search, table or grid,
 * paginated. Everything recomputes from the entries prop, so a changed catalog
 * reshapes the whole view on the next fetch — nothing here is baked.
 */
export function CatalogView({
  entries,
  labels,
}: {
  entries: Entry[]
  labels: Record<string, string>
}) {
  const [filter, setFilter] = useState<CatalogFilter>(emptyFilter)
  const [page, setPage] = useState(1)
  const [grid, setGrid] = useState(false)
  const [withDescriptions, setWithDescriptions] = useState(true)

  const set = (patch: Partial<CatalogFilter>) => {
    setFilter((f) => ({ ...f, ...patch }))
    setPage(1)
  }

  const categories = useMemo(() => {
    const counts = new Map<string, number>()
    for (const e of entries) counts.set(e.category, (counts.get(e.category) ?? 0) + 1)
    return [...counts.entries()].sort((a, b) => b[1] - a[1])
  }, [entries])

  const sources = useMemo(
    () => [...new Set(entries.map((e) => e.source ?? '').filter(Boolean))].sort(),
    [entries],
  )
  const lifecycles = useMemo(
    () => [...new Set(entries.map((e) => e.lifecycle))].sort(),
    [entries],
  )
  // Список строится из того, что реально лежит в каталоге, а не из зашитого
  // перечня: пункт, который ничего не найдёт, хуже отсутствующего.
  const statuses = useMemo(
    () => [...new Set(entries.map((e) => statusOf(e).key))].sort().map(statusStyle),
    [entries],
  )

  const filtered = useMemo(() => sortByDate(filterEntries(entries, filter)), [entries, filter])
  const pages = Math.max(1, Math.ceil(filtered.length / PAGE_SIZE))
  const current = Math.min(page, pages)
  const slice = filtered.slice((current - 1) * PAGE_SIZE, current * PAGE_SIZE)
  const isFiltered = filter !== emptyFilter && JSON.stringify(filter) !== JSON.stringify(emptyFilter)

  return (
    <div className="flex flex-col gap-8 lg:flex-row">
      {/* Sidebar: structure, so square corners and a hairline — not a card. */}
      <aside className="shrink-0 lg:w-64">
        <div className="border border-outline-variant bg-surface-low">
          <button
            type="button"
            onClick={() => set({ category: '' })}
            className={`flex w-full items-center justify-between px-4 py-2.5 text-left text-sm ${
              filter.category === '' ? 'bg-surface-high font-semibold text-secondary' : 'text-on-surface'
            }`}
          >
            <span>Все записи</span>
            <span className="font-mono text-xs tabular-nums">{entries.length}</span>
          </button>
          <div className="max-h-[26rem] overflow-y-auto border-t border-outline-variant lg:max-h-none lg:overflow-visible">
            {categories.map(([cat, n]) => (
              <button
                key={cat}
                type="button"
                onClick={() => set({ category: cat === filter.category ? '' : cat })}
                className={`flex w-full items-center justify-between gap-2 px-4 py-2 text-left text-sm ${
                  filter.category === cat
                    ? 'bg-surface-high font-semibold text-secondary'
                    : 'text-on-surface-variant hover:text-on-surface'
                }`}
              >
                {/* Подсказкой — полная строка из каталога: в ней после
                    двоеточия лежит описание, которое в узкий сайдбар не
                    влезает, но объясняет, что за категория. */}
                <span className="truncate" title={labels[cat] || cat}>
                  {categoryLabel(cat, labels)}
                </span>
                <span className="font-mono text-xs tabular-nums">{n}</span>
              </button>
            ))}
          </div>
        </div>
      </aside>

      <div className="min-w-0 flex-1 space-y-5">
        <header className="flex flex-wrap items-end justify-between gap-3">
          <div>
            <Label className="text-secondary">Архив документов</Label>
            <h1 className="mt-1 text-4xl">Каталог записей.</h1>
            <p className="mt-2 text-sm text-on-surface-variant">
              Централизованный каталог статей, заметок и конспектов. {entries.length} записей в{' '}
              {categories.length} категориях.
            </p>
          </div>
          <div className="flex rounded-md border border-outline-variant">
            {([false, true] as const).map((g) => (
              <button
                key={String(g)}
                type="button"
                onClick={() => setGrid(g)}
                className={`px-3 py-1.5 font-label text-[11px] font-semibold tracking-wider uppercase ${
                  grid === g ? 'bg-surface-high text-on-surface' : 'text-on-surface-variant'
                }`}
              >
                {g ? 'Grid view' : 'Table view'}
              </button>
            ))}
          </div>
        </header>

        <div className="flex flex-wrap items-center gap-3 rounded-lg border border-outline-variant bg-surface-low p-4">
          <input
            value={filter.search}
            onChange={(e) => set({ search: e.target.value })}
            placeholder="Поиск по записям…"
            className={`${selectClass} min-w-40 flex-1`}
          />
          <select value={filter.status} onChange={(e) => set({ status: e.target.value })} className={selectClass}>
            <option value="">Любой статус</option>
            {statuses.map((s) => (
              <option key={s.key} value={s.key}>
                {s.label}
              </option>
            ))}
          </select>
          <select value={filter.source} onChange={(e) => set({ source: e.target.value })} className={selectClass}>
            <option value="">Все источники</option>
            {sources.map((s) => (
              <option key={s} value={s}>
                {s}
              </option>
            ))}
          </select>
          <select
            value={filter.lifecycle}
            onChange={(e) => set({ lifecycle: e.target.value })}
            className={selectClass}
          >
            <option value="">Любой lifecycle</option>
            {lifecycles.map((l) => (
              <option key={l} value={l}>
                {l}
              </option>
            ))}
          </select>
          <label className="flex cursor-pointer items-center gap-2 text-sm text-on-surface-variant">
            <input
              type="checkbox"
              checked={withDescriptions}
              onChange={(e) => setWithDescriptions(e.target.checked)}
              className="accent-[var(--secondary)]"
            />
            Описания
          </label>
          <button
            type="button"
            onClick={() => {
              setFilter(emptyFilter)
              setPage(1)
            }}
            disabled={!isFiltered}
            className="text-sm text-on-surface-variant disabled:opacity-40 hover:text-on-surface"
          >
            Сбросить
          </button>
        </div>

        {grid ? (
          <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
            {slice.map((e) => (
              <div key={e.id} className="flex flex-col gap-2 rounded-lg border border-outline-variant bg-surface-lowest p-4">
                <div className="flex items-center justify-between gap-2">
                  <span className="font-label text-xs text-on-surface-variant">
                    {dateOf(e) || '—'}
                  </span>
                  <Status e={e} />
                </div>
                <a
                  href={e.url || undefined}
                  target="_blank"
                  rel="noreferrer"
                  className="font-headline text-base font-bold hover:underline"
                >
                  {e.title}
                </a>
                {withDescriptions && e.description && (
                  <p className="text-sm text-on-surface-variant">
                    {e.description.length > 150 ? `${e.description.slice(0, 150)}…` : e.description}
                  </p>
                )}
                <Tags tags={e.tags} />
              </div>
            ))}
          </div>
        ) : (
          <div className="overflow-x-auto border border-outline-variant bg-surface-lowest">
            <table className="w-full min-w-[52rem] text-left text-sm">
              <thead className="bg-surface-low">
                <tr>
                  {['Дата', 'Название', 'Теги', 'Категория', 'Статус', ''].map((h) => (
                    <th key={h} className="label px-4 py-3 font-semibold">
                      {h}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {slice.map((e) => (
                  <tr key={e.id} className="border-t border-outline-variant align-top">
                    <td className="whitespace-nowrap px-4 py-4 font-label text-xs text-on-surface-variant">
                      {dateOf(e) || '—'}
                    </td>
                    <td className="max-w-md px-4 py-4">
                      <a
                        href={e.url || undefined}
                        target="_blank"
                        rel="noreferrer"
                        className="font-headline text-base font-bold hover:underline"
                      >
                        {e.title}
                      </a>
                      {withDescriptions && e.description && (
                        <p className="mt-1 text-sm text-on-surface-variant">
                          {e.description.length > 150
                            ? `${e.description.slice(0, 150)}…`
                            : e.description}
                        </p>
                      )}
                    </td>
                    <td className="px-4 py-4">
                      <Tags tags={e.tags} />
                    </td>
                    <td className="px-4 py-4">
                      <span className="whitespace-nowrap rounded-full border border-outline-variant bg-surface-high px-3 py-1 text-xs text-on-surface-variant">
                        {categoryLabel(e.category, labels)}
                      </span>
                    </td>
                    <td className="px-4 py-4">
                      <Status e={e} />
                    </td>
                    <td className="px-4 py-4 text-right">
                      {e.url && (
                        <a
                          href={e.url}
                          target="_blank"
                          rel="noreferrer"
                          className="text-secondary hover:underline"
                          aria-label="Открыть источник"
                        >
                          ↗
                        </a>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}

        <div className="flex flex-wrap items-center justify-between gap-3">
          <span className="label">
            {filtered.length > 0
              ? `Показано ${(current - 1) * PAGE_SIZE + 1}–${Math.min(current * PAGE_SIZE, filtered.length)} из ${filtered.length}`
              : 'Нет записей'}
          </span>
          <Pagination page={current} pages={pages} onPage={setPage} />
        </div>
      </div>
    </div>
  )
}
