import { STRONG_FROM, type GraphLayout } from './graphLayout'

/** Сколько тем показывать в каждом выводе: список длиннее перестаёт быть выводом. */
const TOP = 3

/** Остров считается заметным от десятой доли ядра. Ниже — у категории просто
 * мало записей и мало тегов, и «изолирована» про неё ничего не сообщает. */
const ISLAND_SHARE = 0.1

export interface CoreInsight {
  key: string
  count: number
  /** Доля от всей базы в процентах. */
  share: number
  /** Со сколькими категориями ядро делит теги. */
  linkedCount: number
}

export interface FusedInsight {
  key: string
  count: number
  /** Число общих тегов с ядром. */
  weight: number
}

export interface IslandInsight {
  key: string
  count: number
  /** Сильнейший сосед — или null, если тема не связана вообще ни с чем. */
  linkedTo: string | null
}

export interface GraphInsights {
  core: CoreInsight | null
  fused: FusedInsight[]
  islands: IslandInsight[]
}

/**
 * graphInsights снимает с раскладки три вывода, ради которых на граф и смотрят:
 * что стало ядром базы, какие темы с ним срослись и какие живут островами.
 *
 * Считается из той же раскладки, что нарисована, — читатель не должен сверять
 * текст с картинкой и находить расхождение.
 */
export function graphInsights(layout: GraphLayout, { total }: { total: number }): GraphInsights {
  const hub = layout.nodes.find((n) => n.isHub)
  if (!hub) return { core: null, fused: [], islands: [] }

  const linkedCount = layout.nodes.filter((n) => !n.isHub && n.linkToHub > 0).length

  const core: CoreInsight = {
    key: hub.key,
    count: hub.count,
    share: total > 0 ? Math.round((hub.count / total) * 100) : 0,
    linkedCount,
  }

  const fused = layout.nodes
    .filter((n) => !n.isHub && n.linkToHub >= STRONG_FROM)
    .sort((a, b) => b.linkToHub - a.linkToHub || b.count - a.count)
    .slice(0, TOP)
    .map((n): FusedInsight => ({ key: n.key, count: n.count, weight: n.linkToHub }))

  const islandFloor = hub.count * ISLAND_SHARE
  const islands = layout.nodes
    .filter((n) => !n.isHub && n.linkToHub === 0 && n.count >= islandFloor)
    .sort((a, b) => b.count - a.count)
    .slice(0, TOP)
    .map((n): IslandInsight => ({ key: n.key, count: n.count, linkedTo: strongestNeighbour(layout, n.key) }))

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
