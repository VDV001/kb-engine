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

/** Прямоугольник карточки на поле — то, что отдаёт getBoundingClientRect. */
export interface Box {
  left: number
  top: number
  width: number
  height: number
}

export interface Wire {
  /** Путь кривой в координатах поля. */
  d: string
  /** Середина кривой — туда садится кружок с номером шага. */
  mid: { x: number; y: number }
}

/**
 * Провод от карточки к карточке: кубическая кривая, выходящая вбок.
 *
 * Сторона выхода зависит от направления: шаг назад (адаптер отвечает команде)
 * выходит влево, и без этого две встречные стрелки ложились бы одна на другую,
 * а на карте таких шагов много.
 *
 * Середина считается формулой, а не getPointAtLength: у кубической кривой
 * точка при t=0.5 это (P0 + 3P1 + 3P2 + P3)/8, и тогда номер шага можно
 * поставить, не спрашивая браузер и не имея вообще никакого DOM.
 */
export function wirePath(from: Box, to: Box, field: Box): Wire {
  const forward = to.left >= from.left
  const x1 = (forward ? from.left + from.width : from.left) - field.left
  const y1 = from.top + from.height / 2 - field.top
  const x2 = (forward ? to.left : to.left + to.width) - field.left
  const y2 = to.top + to.height / 2 - field.top

  const dx = Math.max(28, Math.abs(x2 - x1) * 0.45) * (forward ? 1 : -1)
  const c1x = x1 + dx
  const c2x = x2 - dx
  return {
    d: `M ${x1} ${y1} C ${c1x} ${y1}, ${c2x} ${y2}, ${x2} ${y2}`,
    mid: { x: (x1 + 3 * c1x + 3 * c2x + x2) / 8, y: (y1 + 3 * y1 + 3 * y2 + y2) / 8 },
  }
}

// Типы узлов: значок и русское имя. Форма дублирует цвет намеренно — тип узла
// обязан читаться и при слабом различении оттенков.
//
// Наборы у карт разные: у карты кода пять типов, у карты рабочего места
// пятнадцать. Незнакомый тип получает точку и своё же имя, а не выпадает из
// легенды: показать участника без подписи честнее, чем не показать вовсе.
const KINDS: Record<string, { glyph: string; label: string }> = {
  actor: { glyph: '▲', label: 'человек' },
  client: { glyph: '▣', label: 'поверхность' },
  surface: { glyph: '▣', label: 'поверхность' },
  service: { glyph: '●', label: 'код' },
  script: { glyph: '●', label: 'скрипт' },
  engine: { glyph: '■', label: 'движок' },
  job: { glyph: '◷', label: 'расписание' },
  hook: { glyph: '◉', label: 'хук' },
  skill: { glyph: '◎', label: 'скилл' },
  agent: { glyph: '⬟', label: 'агенты' },
  data: { glyph: '◆', label: 'данные' },
  external: { glyph: '◇', label: 'внешний' },
  // Пять состояний, а не сортов. Узел не «такого типа» — он в таком
  // положении, и это находка, а не свойство: скрипт, которого никто не зовёт,
  // выглядит на карте так же, как рабочий.
  'job-missing': { glyph: '⊘', label: 'расписания нет' },
  'script-orphan': { glyph: '⊘', label: 'никто не зовёт' },
  'script-retired': { glyph: '⊗', label: 'выведен из работы' },
  'data-stale': { glyph: '◇', label: 'канал иссяк' },
  'data-missing': { glyph: '◇', label: 'файла нет' },
}

const BROKEN = new Set(['job-missing', 'script-orphan', 'script-retired', 'data-missing', 'data-stale'])

export function kindGlyph(kind?: string): string {
  return KINDS[kind ?? '']?.glyph ?? '•'
}

export function kindLabel(kind?: string): string {
  return KINDS[kind ?? '']?.label ?? kind ?? 'без типа'
}

/** Узел в сломанном положении: задание обещано и не заведено, скрипт без
 * вызывающего, канал, который иссяк. */
export function isBrokenKind(kind?: string): boolean {
  return BROKEN.has(kind ?? '')
}

/**
 * Типы, встречающиеся в карте, для легенды: сначала обычные, потом сломанные —
 * иначе состояние теряется среди сортов.
 */
export function legendKinds(nodes: ArchNode[]): string[] {
  return kindsOf(nodes).sort((a, b) => Number(isBrokenKind(a)) - Number(isBrokenKind(b)))
}

/**
 * Счётчики карты — те же, что печатала страница-предшественник.
 *
 * Половина из них про границу знания, а не про объём: сколько узлов стоит на
 * якоре, сколько шагов несёт источник, сколько узлов в сломанном положении.
 * Объём без них читается как «карта полная».
 */
export function mapCounts(map: Pick<ArchMap, 'nodes' | 'flows' | 'findings' | 'gaps' | 'runtime_checks' | 'stats'>) {
  const steps = map.flows.flatMap((f) => f.steps)
  return {
    nodes: map.nodes.length,
    nodesWithAnchor: map.nodes.filter((n) => (n.sources ?? []).length > 0).length,
    broken: map.nodes.filter((n) => isBrokenKind(n.kind)).length,
    flows: map.flows.length,
    steps: steps.length,
    stepsWithSource: steps.filter((s) => s.source).length,
    unverified: steps.filter((s) => s.unverified).length,
    findings: map.findings.length,
    runtimeChecks: map.runtime_checks.length,
    gaps: map.gaps.length,
  }
}

/**
 * Цвет слоя берётся по его месту в порядке, а не по имени: у двух карт слои
 * называются по-разному («домен» и «хуки и скиллы»), и словарь по именам
 * оставил бы чужую карту серой.
 */
export function laneColor(index: number): string {
  return `var(--lane-${(index % 8) + 1})`
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
