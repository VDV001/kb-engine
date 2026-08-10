import type { ArchFlow, ArchLayer, ArchMap, ArchNode, ArchStep } from './api'
import { plural } from './hygiene'

/** Счётчики карты одним правилом на все панели: «1 сценарий», «3 шага». */
export const counts = {
  flows: (n: number) => `${n} ${plural(n, ['сценарий', 'сценария', 'сценариев'])}`,
  steps: (n: number) => `${n} ${plural(n, ['шаг', 'шага', 'шагов'])}`,
  nodes: (n: number) => `${n} ${plural(n, ['узел', 'узла', 'узлов'])}`,
  findings: (n: number) => `${n} ${plural(n, ['находка', 'находки', 'находок'])}`,
  runs: (n: number) => `${n} ${plural(n, ['прогон', 'прогона', 'прогонов'])}`,
}

/**
 * Правила чтения карты архитектуры — отдельно от разметки, потому что каждое
 * из них проверяется дешевле рендера и переиспользуется несколькими панелями.
 */

/** Якорь карты: «engine:internal/domain/money.go:125» или без имени дерева. */
export interface Anchor {
  /** Имя дерева, если якорь его несёт. Карта рабочего места живёт в трёх
   * деревьях сразу, и без префикса путь некуда развернуть. */
  root?: string
  path: string
  line?: number
  /** Абсолютный путь, если корень известен. Показывается по наведению — на
   * странице, которую AGPL разрешает открыть наружу, печатать чужую файловую
   * систему в тексте не стоит. */
  absolute?: string
}

/**
 * Разбирает якорь. Двоеточий в нём бывает два, и значат они разное: первое
 * отделяет дерево, последнее — строку. Разбор идёт с конца именно поэтому.
 */
export function parseAnchor(raw: string, roots: Record<string, string> = {}): Anchor {
  const trimmed = raw.trim()
  const withLine = /^(.*):(\d+)$/.exec(trimmed)
  const body = withLine ? withLine[1] : trimmed
  const line = withLine ? Number(withLine[2]) : undefined

  const rootSplit = /^([A-Za-z][\w-]*):(.+)$/.exec(body)
  // Путь вида «C:/…» здесь не бывает, а вот «cowork:digest/run.sh» бывает: имя
  // дерева признаётся только тогда, когда оно объявлено в самой карте.
  if (rootSplit && Object.prototype.hasOwnProperty.call(roots, rootSplit[1])) {
    const root = rootSplit[1]
    const path = rootSplit[2]
    return { root, path, line, absolute: joinPath(roots[root], path) }
  }
  return { path: body, line }
}

function joinPath(base: string, rel: string): string {
  return `${base.replace(/\/+$/, '')}/${rel.replace(/^\/+/, '')}`
}

/** Как якорь выглядит в тексте: дерево не повторяем, оно уже в подписи. */
export function anchorLabel(a: Anchor): string {
  return a.line ? `${a.path}:${a.line}` : a.path
}

export interface NodeFilter {
  layer?: string
  kind?: string
  query?: string
}

export function filterNodes(nodes: ArchNode[], f: NodeFilter): ArchNode[] {
  const q = (f.query ?? '').trim().toLowerCase()
  return nodes.filter((n) => {
    if (f.layer && n.layer !== f.layer) return false
    if (f.kind && n.kind !== f.kind) return false
    if (!q) return true
    // Поиск идёт и по якорям: чаще всего сюда приходят с именем файла из
    // трассы, а не с названием узла.
    const hay = [n.title, n.subtitle ?? '', n.id, ...(n.sources ?? [])].join(' ').toLowerCase()
    return hay.includes(q)
  })
}

export interface LayerGroup {
  id: string
  title: string
  nodes: ArchNode[]
}

/**
 * Раскладывает узлы по слоям в объявленном порядке.
 *
 * Узел со слоем, которого в карте нет, не выбрасывается, а собирается в
 * отдельную группу: молча терять участника значит рисовать карту полнее, чем
 * она есть.
 */
export function groupByLayer(nodes: ArchNode[], layers: ArchLayer[]): LayerGroup[] {
  const ordered = [...layers].sort((a, b) => a.order - b.order)
  const groups: LayerGroup[] = ordered.map((l) => ({ id: l.id, title: l.title, nodes: [] }))
  const byID = new Map(groups.map((g) => [g.id, g]))
  const orphans: ArchNode[] = []
  for (const n of nodes) {
    const g = n.layer ? byID.get(n.layer) : undefined
    if (g) g.nodes.push(n)
    else orphans.push(n)
  }
  const out = groups.filter((g) => g.nodes.length > 0)
  if (orphans.length > 0) out.push({ id: '', title: 'Вне объявленных слоёв', nodes: orphans })
  return out
}

export interface FlowFilter {
  zone?: string
  query?: string
  /** Только сценарии, где есть хоть один неподтверждённый шаг. */
  unverifiedOnly?: boolean
}

export function filterFlows(flows: ArchFlow[], f: FlowFilter): ArchFlow[] {
  const q = (f.query ?? '').trim().toLowerCase()
  return flows.filter((fl) => {
    if (f.zone && fl.zone !== f.zone) return false
    if (f.unverifiedOnly && !fl.steps.some((s) => s.unverified)) return false
    if (!q) return true
    const hay = [fl.title, fl.summary ?? '', fl.id, ...fl.steps.map(stepHaystack)].join(' ').toLowerCase()
    return hay.includes(q)
  })
}

function stepHaystack(s: ArchStep): string {
  return [s.call, s.detail ?? '', s.source ?? '', s.symbol ?? ''].join(' ')
}

export function unverifiedCount(flow: ArchFlow): number {
  return flow.steps.filter((s) => s.unverified).length
}

/** Все типы узлов, встречающиеся в карте, в порядке первого появления. */
export function kindsOf(nodes: ArchNode[]): string[] {
  const seen: string[] = []
  for (const n of nodes) {
    if (n.kind && !seen.includes(n.kind)) seen.push(n.kind)
  }
  return seen
}

const SEVERITY_ORDER: Record<string, number> = { high: 0, medium: 1, low: 2 }

/**
 * Порядок находок: сначала тяжесть, внутри неё — открытые перед починенными.
 * Починенная находка остаётся на странице намеренно: она объясняет, почему
 * проверка выглядит именно так, а вычистить её значит потерять причину.
 */
export function sortFindings<T extends { severity?: string; status?: string }>(items: T[]): T[] {
  return [...items].sort((a, b) => {
    const s = (SEVERITY_ORDER[a.severity ?? ''] ?? 3) - (SEVERITY_ORDER[b.severity ?? ''] ?? 3)
    if (s !== 0) return s
    return Number(isFixed(a.status)) - Number(isFixed(b.status))
  })
}

export function isFixed(status?: string): boolean {
  return (status ?? '').toLowerCase().startsWith('починен')
}

/** Словарь корней в виде, удобном для parseAnchor. */
export function rootsOf(map: Pick<ArchMap, 'roots'>): Record<string, string> {
  const out: Record<string, string> = {}
  for (const r of map.roots ?? []) out[r.name] = r.path
  return out
}

/**
 * Что карта говорит о собственной проверенности — одной строкой, тремя
 * состояниями. «Приёмки нет» и «зоны не приняты» — разные ответы: первое
 * значит «сверять не с чем», и зелёная галочка там была бы обещанием.
 */
export function acceptanceState(map: Pick<ArchMap, 'zones' | 'acceptance'>): {
  state: 'accepted' | 'partial' | 'unknown'
  accepted: number
  total: number
} {
  const zones = map.zones ?? []
  const accepted = zones.filter((z) => z.accepted).length
  if (!map.acceptance || accepted === 0) return { state: 'unknown', accepted, total: zones.length }
  return {
    state: accepted === zones.length ? 'accepted' : 'partial',
    accepted,
    total: zones.length,
  }
}
