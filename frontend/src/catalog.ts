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

/**
 * statusOf reduces an entry's three status-ish fields to the one label the
 * catalog shows, the same way the Python dashboard does: the verdict
 * «на подумать» outranks the read state, because it is a decision and the
 * read state is only a bookmark.
 */
export function statusOf(e: Entry): string {
  if (e.verdict === 'napodumat') return 'на подумать'
  if (e.read_state) return e.read_state
  return e.lifecycle
}

export function filterEntries(entries: Entry[], f: CatalogFilter): Entry[] {
  const q = f.search.trim().toLowerCase()
  return entries.filter(
    (e) =>
      (f.category === '' || e.category === f.category) &&
      (f.status === '' || statusOf(e) === f.status) &&
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
