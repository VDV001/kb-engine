import type { Graph } from './api'

export interface GraphNodeBox {
  key: string
  /** Номер в списке — тот же, что показывает боковая панель. */
  index: number
  x: number
  y: number
  width: number
  height: number
  count: number
  isHub: boolean
  /** Число общих тегов с ядром: 0 — тема живёт своим словарём. */
  linkToHub: number
  /** Слой: ядро, сросшиеся с ним темы, острова. */
  ring: 'core' | 'inner' | 'outer'
}

export interface GraphEdgeLine {
  from: string
  to: string
  x1: number
  y1: number
  x2: number
  y2: number
  strokeWidth: number
  opacity: number
  /** Сильная связь рисуется сплошной, слабая — пунктиром. */
  strong: boolean
}

export interface GraphLayout {
  canvasWidth: number
  sideWidth: number
  nodes: GraphNodeBox[]
  edges: GraphEdgeLine[]
}

/** Верхушка каталога: два десятка категорий — предел, после которого холст
 * превращается в войлок, а подписи налезают друг на друга. */
const MAX_NODES = 24

/** Связь считается сильной с трёх общих тегов: одна-две пересекающиеся метки
 * бывают и у соседних тем, три — это уже общая область. */
const STRONG_FROM = 3

function dims(count: number, max: number, hub: boolean): { width: number; height: number } {
  if (hub) return { width: 132, height: 82 }
  const share = max > 0 ? count / max : 0
  if (share > 0.55) return { width: 106, height: 58 }
  if (share > 0.25) return { width: 90, height: 50 }
  return { width: 74, height: 42 }
}

const clamp = (v: number, lo: number, hi: number) => Math.max(lo, Math.min(hi, v))

/**
 * layoutGraph раскладывает граф знаний так же, как исходный дашборд: самая
 * крупная категория в центре, остальные — двумя эллиптическими кольцами, чтобы
 * заполнить и середину холста, и края.
 *
 * Позиция берётся из порядкового номера, а не из симуляции: та же база даёт ту
 * же картинку, и сегодняшний снимок сравним со вчерашним.
 */
export function layoutGraph(
  graph: Graph,
  box: { width: number; height: number },
  maxNodes = MAX_NODES,
): GraphLayout {
  // Панель забирает четверть ширины, но не больше 240: на широком экране ей
  // столько не нужно, а на узком она иначе съедает холст.
  const sideWidth = Math.min(240, box.width * 0.26)
  const canvasWidth = box.width - sideWidth
  const shown = graph.nodes.slice(0, maxNodes)
  if (shown.length === 0) return { canvasWidth, sideWidth, nodes: [], edges: [] }

  const cx = canvasWidth / 2
  const cy = box.height / 2
  const max = Math.max(1, ...shown.map((n) => n.count))
  const hubKey = shown[0].category

  // Связь с ядром — вес ребра в любую сторону. Ребро мимо ядра к центру не
  // приближает: слой отвечает на вопрос «насколько тема сплетена с главной
  // линией», а не «насколько она вообще с кем-то связана».
  const linkToHub = new Map<string, number>()
  for (const e of graph.edges) {
    if (e.from === hubKey) linkToHub.set(e.to, Math.max(linkToHub.get(e.to) ?? 0, e.weight))
    else if (e.to === hubKey) linkToHub.set(e.from, Math.max(linkToHub.get(e.from) ?? 0, e.weight))
  }

  // Порядок по силе связи, при равной — по размеру (входной порядок).
  // Сортировка устойчивая, поэтому картинка остаётся детерминированной.
  const orbit = shown
    .slice(1)
    .map((n, i) => ({ node: n, order: i, link: linkToHub.get(n.category) ?? 0 }))
    .sort((a, b) => b.link - a.link || a.order - b.order)

  // Внутреннее кольцо короче внешнего: у него дуга меньше, и класть на неё
  // половину узлов значит сталкивать прямоугольники.
  const innerCount = Math.ceil(orbit.length * 0.45)
  const place = new Map(orbit.map((o, rank) => [o.node.category, rank]))

  const nodes = shown.map((n, i): GraphNodeBox => {
    const hub = i === 0
    const { width, height } = dims(n.count, max, hub)
    const link = linkToHub.get(n.category) ?? 0
    let x = cx
    let y = cy
    let ring: GraphNodeBox['ring'] = 'core'
    if (!hub) {
      const r = place.get(n.category) ?? 0
      const inner = r < innerCount
      ring = inner ? 'inner' : 'outer'
      const list = inner ? innerCount : orbit.length - innerCount
      const at = inner ? r : r - innerCount
      // Внешнее кольцо сдвинуто на полшага, чтобы его узлы вставали в
      // промежутки внутреннего, а не в затылок им.
      const angle = (2 * Math.PI * at) / list - Math.PI / 2 + (inner ? 0 : Math.PI / list)
      const rx = canvasWidth * (inner ? 0.32 : 0.47)
      const ry = box.height * (inner ? 0.3 : 0.43)
      x = cx + rx * Math.cos(angle)
      y = cy + ry * Math.sin(angle)
    }
    return {
      key: n.category,
      index: i + 1,
      // Прямоугольник целиком внутри холста: вылезший обрезается контейнером,
      // и обрезанная подпись читается как поломка вёрстки при целых данных.
      x: clamp(x, width / 2, canvasWidth - width / 2),
      y: clamp(y, height / 2, box.height - height / 2),
      width,
      height,
      count: n.count,
      isHub: hub,
      linkToHub: link,
      ring,
    }
  })

  const at = new Map(nodes.map((n) => [n.key, n]))
  const maxWeight = Math.max(1, ...graph.edges.map((e) => e.weight))
  const edges = graph.edges.flatMap((e): GraphEdgeLine[] => {
    const a = at.get(e.from)
    const b = at.get(e.to)
    // Сервер считает связи по всему каталогу, а на холст попадает верхушка:
    // ребро в невидимую категорию рисовать не из чего.
    if (!a || !b) return []
    const share = e.weight / maxWeight
    return [
      {
        from: e.from,
        to: e.to,
        x1: a.x,
        y1: a.y,
        x2: b.x,
        y2: b.y,
        strokeWidth: 0.5 + share * 1.3,
        opacity: 0.07 + share * 0.3,
        strong: e.weight >= STRONG_FROM,
      },
    ]
  })

  return { canvasWidth, sideWidth, nodes, edges }
}
