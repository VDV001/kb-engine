import type { Graph } from './api'
import { categoryLabel } from './catalog'

/** Ключ неупорядоченной пары: подпись и ребро описывают одну связь
 * независимо от того, в какую сторону каждое записано. */
export function pairKey(a: string, b: string): string {
  return a < b ? `${a}|${b}` : `${b}|${a}`
}

/** Одна связь, которую владелец подписал руками, со всеми её смыслами. */
export interface CuratedRow {
  from: string
  to: string
  /** Читаемые названия категорий — то, что видно на холсте. */
  fromLabel: string
  toLabel: string
  labels: string[]
}

/**
 * curatedRows собирает подписанные связи для списка под графом.
 *
 * Список нужен потому, что подписи не помещаются на холст: двести сорок пять
 * линий и подпись длиной в предложение — это войлок. На холсте подписанная
 * связь выделяется, а что именно на ней написано, читается здесь.
 */
export function curatedRows(graph: Graph, labels: Record<string, string>): CuratedRow[] {
  return graph.edges
    .filter((e) => (e.labels?.length ?? 0) > 0)
    .map((e) => ({
      from: e.from,
      to: e.to,
      fromLabel: categoryLabel(e.from, labels),
      toLabel: categoryLabel(e.to, labels),
      labels: e.labels ?? [],
    }))
    .sort((a, b) => b.labels.length - a.labels.length || a.fromLabel.localeCompare(b.fromLabel))
}

/**
 * coverageNote — строка, которая не даёт принять весь граф за размеченный
 * вручную. Без неё несколько выделенных связей читаются как «здесь всё
 * продумано», хотя подписана меньше десятой части.
 */
export function coverageNote(graph: Graph): string {
  const total = graph.edges.length
  const labeled = graph.labeled ?? 0
  const count = graph.label_count ?? 0
  if (total === 0) return 'Связей нет.'
  if (labeled === 0) {
    return `Все ${total} связей выведены автоматически по общим тегам — подписей нет ни на одной.`
  }
  const meanings = count > labeled ? `, ${count} подписей` : ''
  return `Подписано вручную ${labeled} связей из ${total}${meanings}. Остальные выведены автоматически по общим тегам.`
}

/**
 * unplacedNote сообщает о подписях, которым не нашлось связи. Молчать про них
 * нельзя: это утверждение владельца, которое движок не смог разместить, и
 * пустая строка на экране означала бы, что его никто не потерял.
 */
export function unplacedNote(graph: Graph): string | null {
  const n = graph.unplaced_links?.length ?? 0
  if (n === 0) return null
  return `${n} подписанных связей не нашли пары в вычисленном графе — они есть в конфиге, но категории не пересекаются по тегам.`
}
