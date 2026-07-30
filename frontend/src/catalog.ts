import type { Entry } from './api'

// Pure logic behind the catalog view, kept out of the component so the rules
// that decide what a person sees are testable without rendering anything.

export interface CatalogFilter {
  category: string
  status: string
  source: string
  lifecycle: string
  search: string
}

export const emptyFilter: CatalogFilter = {
  category: '',
  status: '',
  source: '',
  lifecycle: '',
  search: '',
}

/** One status as the catalog shows it: the value filters match on, the words a
 * person reads, and the colour both the dot and the caption take. */
export interface StatusView {
  key: string
  label: string
  tone: string
}

// Подписи и тона статусов — один источник на всё приложение. Раньше они лежали
// двумя словарями внутри CatalogView, и таблица с сеткой уже расходились в
// старом дашборде именно потому, что копий было две.
const STATUS_STYLE: Record<string, [tone: string, label: string]> = {
  keep: ['var(--status-keep)', 'KEEP'],
  napodumat: ['var(--status-napodumat)', 'На подумать'],
  skip: ['var(--on-surface-variant)', 'SKIP'],
  'skip-unavailable': ['var(--on-surface-variant)', 'SKIP · нет доступа'],
  unread: ['var(--status-published)', 'Unread'],
  read: ['var(--status-review)', 'Прочитано'],
}

/**
 * statusOf reduces an entry's status-ish fields to the one status the catalog
 * shows. Order is the point: a verdict outranks the read state, because the
 * reader reads an article in order to decide about it — «прочитано» next to a
 * recorded verdict says nothing the verdict has not already said. A publish
 * stage comes next: owner creations never go through triage at all. Lifecycle
 * is the last resort, not the second one.
 */
export function statusOf(e: Entry): StatusView {
  const key = e.verdict || e.read_state || e.publish_stage || e.lifecycle
  return statusStyle(key)
}

/** statusStyle keeps an unrecognised value visible and verbatim: the tone stays
 * readable and the caption prints the value itself. Nine entries still carry
 * statuses from older vocabularies, and hiding them would hide the cleanup they
 * are asking for — status-draft (#c9c4bc on #fbf9f2, contrast 1.6) hides them. */
export function statusStyle(key: string): StatusView {
  const hit = STATUS_STYLE[key.trim().toLowerCase()]
  if (hit) return { key, label: hit[1], tone: hit[0] }
  return { key, label: key.trim() || '—', tone: 'var(--on-surface-variant)' }
}

export function filterEntries(entries: Entry[], f: CatalogFilter): Entry[] {
  const q = f.search.trim().toLowerCase()
  return entries.filter(
    (e) =>
      (f.category === '' || e.category === f.category) &&
      (f.status === '' || statusOf(e).key === f.status) &&
      (f.source === '' || (e.source ?? '') === f.source) &&
      (f.lifecycle === '' || e.lifecycle === f.lifecycle) &&
      (q === '' ||
        e.title.toLowerCase().includes(q) ||
        (e.description ?? '').toLowerCase().includes(q) ||
        (e.tags ?? []).some((t) => t.toLowerCase().includes(q))),
  )
}

/** Newest first; entries without a date sink to the bottom in id order, so the
 * bot-imported tail without dates does not shuffle randomly. */
export function sortByDate(entries: Entry[]): Entry[] {
  return [...entries].sort((a, b) => {
    if (a.date_added && b.date_added) return b.date_added.localeCompare(a.date_added)
    if (a.date_added) return -1
    if (b.date_added) return 1
    return b.id - a.id
  })
}

/**
 * pageWindow is the shape every pagination in the product uses: the current
 * page with its right neighbour, the last two pages, one ellipsis between the
 * sides (null in the returned list). Never a leading ellipsis — the ‹ arrow
 * already says there are pages before.
 */
export function pageWindow(current: number, total: number): (number | null)[] {
  const wanted = new Set([current, current + 1, total - 1, total])
  const shown = [...wanted].filter((p) => p >= 1 && p <= total).sort((a, b) => a - b)
  const out: (number | null)[] = []
  let prev = shown[0]
  for (const p of shown) {
    if (p - prev > 1) out.push(null)
    out.push(p)
    prev = p
  }
  return out
}
