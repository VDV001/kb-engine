import type { Entry } from './api'
import type { GraphLayout } from './graphLayout'

/** Сколько тем показывать в каждом выводе: список длиннее перестаёт быть выводом. */
const TOP = 3

/** Категория мельче процента базы в выводы не идёт: восемь общих тегов на две
 * записи говорят о том, что у записи много меток, а не о близости темы. */
const NOTABLE_SHARE = 0.01

/** С одного общего тега на запись тема считается тесно связанной с ядром. */
const CLOSE_PER_ENTRY = 1

export interface CoreInsight {
  key: string
  count: number
  /** Доля от всей базы в процентах. */
  share: number
  /** Сколько категорий связаны с ядром тесно — то есть от тега на запись. */
  closeCount: number
  /** Сколько категорий вообще на холсте, кроме ядра. */
  peerCount: number
  /** Сколько разных тегов у самого ядра — размер его словаря. */
  vocabulary: number
}

export interface LinkInsight {
  key: string
  count: number
  /** Число общих тегов с ядром. */
  weight: number
  /** Общих тегов на одну запись — по этому и отбираем. */
  perEntry: number
  /** Сильнейший сосед. Для острова — то, куда его тянет, если не к ядру. */
  linkedTo: string | null
  /** Самые частые теги, общие с ядром: из чего связь на самом деле состоит. */
  sharedTags: string[]
  /** Самые частые теги, которых у ядра нет вовсе — то, что держит тему в стороне. */
  ownTags: string[]
}

export interface GraphInsights {
  core: CoreInsight | null
  fused: LinkInsight[]
  islands: LinkInsight[]
}

/**
 * graphInsights снимает с раскладки три вывода, ради которых на граф и смотрят:
 * что стало ядром базы, какие темы с ним срослись и какие живут островами.
 *
 * Отбор идёт по числу общих тегов НА ЗАПИСЬ, а не по их общему числу. Замер по
 * живой базе показал, почему: абсолютная величина там почти повторяет размер
 * категории (крупнейшая делит с ядром 95 тегов, мельчайшая — 4), и по ней ядро
 * оказывается связано вообще со всеми, а островов не существует. Плотность же
 * различает уверенно — от 3.4 тега на запись до 0.3.
 *
 * Считается из той же раскладки, что нарисована: читатель не должен сверять
 * текст с картинкой и находить расхождение.
 */
export function graphInsights(
  layout: GraphLayout,
  { total, entries = [] }: { total: number; entries?: Entry[] },
): GraphInsights {
  const hub = layout.nodes.find((n) => n.isHub)
  if (!hub) return { core: null, fused: [], islands: [] }

  const peers = layout.nodes.filter((n) => !n.isHub)
  const notable = total * NOTABLE_SHARE
  const vocab = tagsByCategory(entries)
  const hubTags = vocab.get(hub.key) ?? new Map<string, number>()

  const ranked = peers
    .filter((n) => n.count >= notable)
    .map((n): LinkInsight => {
      const own = vocab.get(n.key) ?? new Map<string, number>()
      return {
        key: n.key,
        count: n.count,
        weight: n.linkToHub,
        perEntry: n.count > 0 ? n.linkToHub / n.count : 0,
        linkedTo: strongestNeighbour(layout, n.key),
        sharedTags: frequentTags(own, (tag) => hubTags.has(tag)),
        ownTags: frequentTags(own, (tag) => !hubTags.has(tag)),
      }
    })
    .sort((a, b) => b.perEntry - a.perEntry || b.count - a.count)

  const core: CoreInsight = {
    key: hub.key,
    count: hub.count,
    share: total > 0 ? Math.round((hub.count / total) * 100) : 0,
    closeCount: ranked.filter((n) => n.perEntry >= CLOSE_PER_ENTRY).length,
    peerCount: peers.length,
    vocabulary: hubTags.size,
  }

  // Верх и низ одного списка. Когда заметных категорий мало, половины могли бы
  // пересечься — тогда одна тема попала бы разом в сросшиеся и в острова.
  const half = Math.floor(ranked.length / 2)
  const fused = ranked.slice(0, Math.min(TOP, half))
  const islands = ranked.slice(-Math.min(TOP, ranked.length - fused.length)).reverse()

  return { core, fused, islands }
}

/** С кем тема связана крепче всего — то есть куда её тянет, если не к ядру. */
function strongestNeighbour(layout: GraphLayout, key: string): string | null {
  let best: { key: string; weight: number } | null = null
  for (const e of layout.edges) {
    const other = e.from === key ? e.to : e.to === key ? e.from : null
    if (other === null) continue
    if (!best || e.weight > best.weight) best = { key: other, weight: e.weight }
  }
  return best?.key ?? null
}

/** Сколько тегов показывать в карточке: больше — уже не пример, а список. */
const TAG_SAMPLE = 3

/** Теги каждой категории с их частотой. */
function tagsByCategory(entries: Entry[]): Map<string, Map<string, number>> {
  const out = new Map<string, Map<string, number>>()
  for (const e of entries) {
    let bucket = out.get(e.category)
    if (!bucket) {
      bucket = new Map<string, number>()
      out.set(e.category, bucket)
    }
    for (const tag of e.tags ?? []) bucket.set(tag, (bucket.get(tag) ?? 0) + 1)
  }
  return out
}

/** Самые частые теги, прошедшие отбор. Порядок при равной частоте — алфавитный,
 * иначе карточка меняется от запроса к запросу без правок в каталоге. */
function frequentTags(tags: Map<string, number>, keep: (tag: string) => boolean): string[] {
  return [...tags.entries()]
    .filter(([tag]) => keep(tag))
    .sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]))
    .slice(0, TAG_SAMPLE)
    .map(([tag]) => tag)
}
