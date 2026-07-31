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
 * categoryLabel turns a category key into the name a person reads. The catalog
 * stores «Название: описание» on one line; a list wants the name, and the
 * description belongs in a tooltip. An undescribed category falls back to its
 * own key rather than to a made-up name — a missing entry in the naming is
 * something to notice, not to paper over.
 */
export function categoryLabel(key: string, labels: Record<string, string>): string {
  const full = labels[key]
  if (!full) return key
  return full.split(':')[0].trim()
}

/**
 * tagLabel turns a tag key into the name a person reads. Unlike a category,
 * a tag has no description to strip: the whole label is the name. Most tags
 * carry no label at all and do not need one — the key is already readable
 * («mcp», «claude-code»). Labels exist for the two dozen keys that replaced
 * Russian tags, where the key alone would no longer say what the tag means.
 */
export function tagLabel(key: string, labels: Record<string, string>): string {
  return labels[key] ?? key
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
  consider: ['var(--status-napodumat)', 'На подумать'],
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

/**
 * dateOf is the one date the catalog shows. Entries carry one of two fields and
 * almost never both: date_added for what the bot and the owner filed away,
 * date_created for the owner's own material. Reading a single field left a
 * third of the archive dateless — not because the date was missing, but because
 * the view looked in the other place. When both exist, the archive column means
 * «when this joined the base», so date_added wins.
 */
export function dateOf(e: Entry): string {
  return e.date_added || e.date_created || ''
}

/** Newest first; entries without a date sink to the bottom in id order, so the
 * dateless tail does not shuffle randomly. Ties break by id descending, the way
 * the source dashboard does it — a batch import shares one date across dozens
 * of entries, and without the tiebreak their order is whatever sort felt like. */
export function sortByDate(entries: Entry[]): Entry[] {
  return [...entries].sort((a, b) => {
    const [da, db] = [dateOf(a), dateOf(b)]
    if (da && db) return db.localeCompare(da) || b.id - a.id
    if (da) return -1
    if (db) return 1
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

export interface TagWeight {
  tag: string
  count: number
  /** Место тега в выборке: 0 — самый редкий из показанных, 1 — самый частый. */
  scale: number
}

/**
 * topTags — самые частые теги каталога для облака.
 *
 * Масштаб нормализуется внутри выборки, а не по всему словарю: у базы длинный
 * хвост из тегов-одиночек, и по нему верхние два десятка различались бы только
 * в последних процентах размера.
 */
export function topTags(entries: Entry[], limit: number): TagWeight[] {
  const counts = new Map<string, number>()
  for (const e of entries) {
    for (const tag of e.tags ?? []) {
      counts.set(tag, (counts.get(tag) ?? 0) + 1)
    }
  }

  const top = [...counts.entries()]
    .sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]))
    .slice(0, limit)
  if (top.length === 0) return []

  const most = top[0][1]
  const least = top[top.length - 1][1]
  const span = most - least
  return top.map(([tag, count]) => ({
    tag,
    count,
    scale: span === 0 ? 1 : (count - least) / span,
  }))
}
