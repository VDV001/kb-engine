import type { DocCard } from './api'

/**
 * Раскладка потока задач по ярусам. Секция team.json описывает движение работы
 * между участниками: карточки — рёбра, а не шаги, и порядок в файле не равен
 * расстоянию от входа. Здесь из них собирается схема; рисование — в DocViews.
 */

/** Участник. Ярус — расстояние от входа по задачам, не номер в файле. */
export interface FlowNode {
  id: string
  tier: number
  x: number
  y: number
  width: number
  height: number
}

export interface FlowEdge {
  from: string
  to: string
  kind: 'task' | 'status'
  /** Карточка, породившая ребро: по клику показывается её описание. */
  card: DocCard
}

export interface FlowLayout {
  nodes: FlowNode[]
  edges: FlowEdge[]
  width: number
  height: number
}

/**
 * Концы потока: кому работу никто не ставит (вход) и кто её дальше не передаёт
 * (исполнитель). Считаются по задачам — статус наверх это отчёт, а не работа,
 * ушедшая кому-то ещё.
 *
 * Нужны легенде и выводятся из схемы, а не пишутся рядом руками: отдельный
 * список разошёлся бы с картинкой в первый же день, когда в файле появится
 * новый участник.
 */
export function flowEnds(flow: FlowLayout): { sources: string[]; sinks: string[] } {
  const task = flow.edges.filter((e) => e.kind === 'task')
  const incoming = new Set(task.map((e) => e.to))
  const outgoing = new Set(task.map((e) => e.from))
  const ids = flow.nodes.map((n) => n.id)
  return {
    sources: ids.filter((id) => !incoming.has(id)),
    sinks: ids.filter((id) => !outgoing.has(id)),
  }
}

const NODE_WIDTH = 168
const NODE_HEIGHT = 44
const GAP_X = 28
const TIER_HEIGHT = 108

function pairs(card: DocCard): Array<[string, string]> {
  const { from, to, via } = card
  if (!from || !to) return []
  return via ? [[from, via], [via, to]] : [[from, to]]
}

/**
 * Ярусы по длиннейшему пути от входа. Считаются только задачи: статус идёт
 * назад по той же паре, и учитывать его значило бы растянуть двух участников
 * на четыре яруса вместо двух.
 *
 * Проходов не больше, чем узлов: цикл в задачах — ошибка в данных владельца, но
 * вкладка от неё виснуть не должна. Дойдя до предела, раскладка оставляет то,
 * что успела насчитать, и рисует схему как есть.
 */
function tiers(nodes: string[], edges: FlowEdge[]): Map<string, number> {
  const tier = new Map(nodes.map((id) => [id, 0]))
  const task = edges.filter((e) => e.kind === 'task')

  for (let pass = 0; pass < nodes.length; pass++) {
    let moved = false
    for (const e of task) {
      const next = tier.get(e.from)! + 1
      if (next > tier.get(e.to)!) {
        tier.set(e.to, next)
        moved = true
      }
    }
    if (!moved) break
  }
  return tier
}

export function layoutFlow(cards: DocCard[]): FlowLayout {
  const edges: FlowEdge[] = []
  const order: string[] = []

  const see = (id: string) => {
    if (!order.includes(id)) order.push(id)
  }

  for (const card of cards) {
    for (const [from, to] of pairs(card)) {
      see(from)
      see(to)
      edges.push({ from, to, kind: card.kind ?? 'task', card })
    }
  }

  const tier = tiers(order, edges)

  // Внутри яруса порядок — тот, в котором участники встретились в файле: он
  // отражает, как владелец сам рассказывает про поток.
  const byTier = new Map<number, string[]>()
  for (const id of order) {
    const t = tier.get(id)!
    byTier.set(t, [...(byTier.get(t) ?? []), id])
  }

  const widest = Math.max(1, ...[...byTier.values()].map((row) => row.length))
  const width = widest * NODE_WIDTH + (widest - 1) * GAP_X
  const tierCount = byTier.size

  const nodes: FlowNode[] = order.map((id) => {
    const t = tier.get(id)!
    const row = byTier.get(t)!
    const rowWidth = row.length * NODE_WIDTH + (row.length - 1) * GAP_X
    const left = (width - rowWidth) / 2
    return {
      id,
      tier: t,
      x: left + row.indexOf(id) * (NODE_WIDTH + GAP_X),
      y: t * TIER_HEIGHT,
      width: NODE_WIDTH,
      height: NODE_HEIGHT,
    }
  })

  return {
    nodes,
    edges,
    width: order.length === 0 ? 0 : width,
    height: order.length === 0 ? 0 : (tierCount - 1) * TIER_HEIGHT + NODE_HEIGHT,
  }
}
