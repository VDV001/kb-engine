// Гигиена каталога: перевод машинных находок движка в то, что читает владелец.
//
// Audit и dedup отдают коды — `verdict:skip-unavailable`, `referenced by 4
// entries`, `similar-title`. Коды писались для команды в терминале, где рядом
// стоит исходник. На странице рядом с ними стоит человек, который заглядывает
// сюда раз в месяц, поэтому весь перевод собран здесь, а не размазан по виду.

import type { Audits, DuplicateGroup, Entry, Finding } from './api'

/** Три формы русского существительного: 1 запись, 2 записи, 5 записей. */
export type PluralForms = [one: string, few: string, many: string]

// Правила склонения живут в платформе с ES2018 — своя арифметика по остаткам
// от 10 и 100 была бы четвёртой копией того, что уже есть в Intl.
const ruPlural = new Intl.PluralRules('ru')

export function plural(n: number, forms: PluralForms): string {
  const rule = ruPlural.select(n)
  if (rule === 'one') return forms[0]
  if (rule === 'few') return forms[1]
  return forms[2]
}

const ENTRIES: PluralForms = ['запись', 'записи', 'записей']
const MONTHS: PluralForms = ['месяц', 'месяца', 'месяцев']

/** Причины, у которых нет переменной части. */
const REASONS: Record<string, string> = {
  'verdict:skip-unavailable': 'Автор снял статью',
  'marked superseded but no entry supersedes it': 'Помечена заменённой, но замены нет',
  'supersedes_id forms a cycle': 'Замены образуют цикл',
}

/** Причины с числом или словом внутри. */
const PATTERNS: [RegExp, (m: RegExpMatchArray) => string][] = [
  [/^keyword:(.+)$/, (m) => `В тексте: ${m[1]}`],
  [
    /^referenced by (\d+) entries$/,
    (m) => {
      const n = Number(m[1])
      const verb = plural(n, ['ссылается', 'ссылаются', 'ссылаются'])
      return `На неё ${verb} ${n} ${plural(n, ENTRIES)}`
    },
  ],
  [/^supersedes_id (\d+) does not exist$/, (m) => `Замена указана на запись ${m[1]}, которой нет`],
  [
    /^supersedes_id (\d+) is not marked superseded$/,
    (m) => `Запись ${m[1]} названа заменённой, но статус у неё другой`,
  ],
  [
    /^habr article older than (\d+) months \(created (.+)\)$/,
    (m) => `Статья с Хабра старше ${m[1]} ${plural(Number(m[1]), MONTHS)} (от ${m[2]})`,
  ],
]

/**
 * reasonLabel переводит код причины. Незнакомый код возвращается как есть:
 * показать машинный вид неприятно, но это видно и чинится, а тихо проглотить
 * причину — значит потерять работу молча.
 */
export function reasonLabel(code: string): string {
  const known = REASONS[code]
  if (known) return known
  for (const [re, render] of PATTERNS) {
    const m = code.match(re)
    if (m) return render(m)
  }
  return code
}

const DUPLICATE_KINDS: Record<string, string> = {
  'exact-url': 'Один и тот же адрес',
  'similar-title': 'Похожие заголовки',
}

export function duplicateKindLabel(kind: string): string {
  return DUPLICATE_KINDS[kind] ?? kind
}

export interface ReasonGroup {
  code: string
  label: string
  items: Finding[]
}

/**
 * groupByReason сводит находки в группы по ПЕРВОЙ причине. Первая, а не все:
 * иначе запись с двумя причинами попадёт в две группы, и сумма размеров
 * перестанет сходиться с числом находок. Остальные причины остаются на самой
 * строке — они уточняют находку, а не создают отдельную работу.
 */
export function groupByReason(findings: Finding[] | null): ReasonGroup[] {
  const byCode = new Map<string, Finding[]>()
  for (const f of findings ?? []) {
    const code = f.Reasons[0] ?? ''
    const bucket = byCode.get(code)
    if (bucket) bucket.push(f)
    else byCode.set(code, [f])
  }
  return [...byCode.entries()]
    .map(([code, items]) => ({ code, label: code === '' ? 'Без причины' : reasonLabel(code), items }))
    .sort((a, b) => b.items.length - a.items.length)
}

/** Записи группы дублей — в порядке её id, без тех, кого в каталоге уже нет. */
export function duplicateEntries(group: DuplicateGroup, entries: Entry[]): Entry[] {
  const byID = new Map(entries.map((e) => [e.id, e]))
  return group.EntryIDs.map((id) => byID.get(id)).filter((e): e is Entry => e !== undefined)
}

/**
 * conflictingIds — записи, попавшие больше чем в один раздел аудита. На живом
 * каталоге это #481: движок предлагает пометить её и устаревшей, и
 * канонической. Два совета, которые нельзя выполнить оба, — это не два совета,
 * а один вопрос, и задать его надо явно.
 */
export function conflictingIds(audits: Audits): number[] {
  const seen = new Map<number, number>()
  for (const section of [audits.outdated, audits.canonical, audits.supersession]) {
    for (const id of new Set((section ?? []).map((f) => f.EntryID))) {
      seen.set(id, (seen.get(id) ?? 0) + 1)
    }
  }
  return [...seen.entries()].filter(([, n]) => n > 1).map(([id]) => id)
}

/** Объём работы для счётчика во вкладке: находки аудита плюс группы дублей. */
export function findingCount(audits: Audits, duplicates: DuplicateGroup[]): number {
  const sections = [audits.outdated, audits.canonical, audits.supersession]
  // Группы дублей считаются так же осторожно, как разделы аудита. Асимметрия
  // ровно здесь и стоила белого экрана: разделы были защищены, список групп нет.
  return sections.reduce((n, s) => n + (s?.length ?? 0), 0) + (duplicates?.length ?? 0)
}
