import { describe, expect, it } from 'vitest'
import type { Audits, DuplicateGroup, Entry, Finding } from './api'
import {
  conflictingIds,
  duplicateEntries,
  duplicateKindLabel,
  findingCount,
  groupByReason,
  plural,
  reasonLabel,
} from './hygiene'

function finding(id: number, reasons: string[], title = `t${id}`): Finding {
  return { EntryID: id, Title: title, Current: 'active', Reasons: reasons }
}

function entry(id: number, over: Partial<Entry> = {}): Entry {
  return {
    id,
    title: `Заголовок ${id}`,
    category: 'ai',
    kind: 'article',
    lifecycle: 'active',
    ...over,
  }
}

describe('reasonLabel', () => {
  // Причины приходят машинными кодами. На экране их читает владелец базы, а не
  // тот, кто писал audit.go, поэтому код переводится, а не показывается.
  const cases: [string, string][] = [
    ['verdict:skip-unavailable', 'Автор снял статью'],
    ['keyword:403', 'В тексте: 403'],
    ['keyword:deprecated', 'В тексте: deprecated'],
    ['keyword:снято', 'В тексте: снято'],
    ['referenced by 4 entries', 'На неё ссылаются 4 записи'],
    ['referenced by 1 entries', 'На неё ссылается 1 запись'],
    ['referenced by 5 entries', 'На неё ссылаются 5 записей'],
    ['marked superseded but no entry supersedes it', 'Помечена заменённой, но замены нет'],
    ['supersedes_id 7 does not exist', 'Замена указана на запись 7, которой нет'],
    ['supersedes_id forms a cycle', 'Замены образуют цикл'],
    ['supersedes_id 7 is not marked superseded', 'Запись 7 названа заменённой, но статус у неё другой'],
    [
      'habr article older than 18 months (created 2024-01-01)',
      'Статья с Хабра старше 18 месяцев (от 2024-01-01)',
    ],
  ]
  it.each(cases)('%s → %s', (code, want) => {
    expect(reasonLabel(code)).toBe(want)
  })

  // Незнакомый код показывается как есть: тихо проглотить причину хуже, чем
  // показать её машинным видом — второе хотя бы видно и чинится.
  it('незнакомый код возвращается без изменений', () => {
    expect(reasonLabel('whatever:new')).toBe('whatever:new')
  })
})

describe('plural', () => {
  const forms: [string, string, string] = ['группа', 'группы', 'групп']
  const cases: [number, string][] = [
    [0, 'групп'],
    [1, 'группа'],
    [2, 'группы'],
    [4, 'группы'],
    [5, 'групп'],
    [11, 'групп'],
    [12, 'групп'],
    [21, 'группа'],
    [22, 'группы'],
    [25, 'групп'],
    [101, 'группа'],
    [111, 'групп'],
  ]
  it.each(cases)('%i → %s', (n, want) => {
    expect(plural(n, forms)).toBe(want)
  })
})

describe('groupByReason', () => {
  // Пятьдесят одна одинаковая плашка в столбик — это одна причина, размазанная
  // по списку. Группировка идёт по первой причине находки, поэтому сумма
  // размеров групп равна числу находок: запись не задваивается.
  it('сводит находки в группы по первой причине', () => {
    const groups = groupByReason([
      finding(1, ['verdict:skip-unavailable']),
      finding(2, ['keyword:deprecated', 'verdict:skip-unavailable']),
      finding(3, ['verdict:skip-unavailable']),
    ])
    expect(groups.map((g) => [g.code, g.items.length])).toEqual([
      ['verdict:skip-unavailable', 2],
      ['keyword:deprecated', 1],
    ])
    expect(groups.reduce((n, g) => n + g.items.length, 0)).toBe(3)
  })

  it('крупные группы идут первыми, внутри — исходный порядок', () => {
    const groups = groupByReason([
      finding(1, ['keyword:403']),
      finding(2, ['verdict:skip-unavailable']),
      finding(3, ['verdict:skip-unavailable']),
    ])
    expect(groups[0].code).toBe('verdict:skip-unavailable')
    expect(groups[0].items.map((f) => f.EntryID)).toEqual([2, 3])
  })

  it('несёт готовую подпись группы', () => {
    expect(groupByReason([finding(1, ['verdict:skip-unavailable'])])[0].label).toBe(
      'Автор снял статью',
    )
  })

  it('находка без причин попадает в отдельную группу, а не теряется', () => {
    const groups = groupByReason([finding(1, [])])
    expect(groups).toHaveLength(1)
    expect(groups[0].items.map((f) => f.EntryID)).toEqual([1])
  })

  it('пустой и отсутствующий список дают ноль групп', () => {
    expect(groupByReason([])).toEqual([])
    expect(groupByReason(null)).toEqual([])
  })
})

describe('duplicateEntries', () => {
  const group: DuplicateGroup = { Kind: 'similar-title', Key: 'k', EntryIDs: [2, 1] }

  it('поднимает записи каталога по id группы', () => {
    const got = duplicateEntries(group, [entry(1), entry(2), entry(3)])
    expect(got.map((e) => e.id)).toEqual([2, 1])
  })

  // Решать «дубль или нет» по одним id нельзя. Если записи в каталоге уже нет,
  // группа всё равно должна показать то, что есть, а не исчезнуть целиком.
  it('пропускает id, которых нет в каталоге', () => {
    expect(duplicateEntries(group, [entry(1)]).map((e) => e.id)).toEqual([1])
  })
})

describe('duplicateKindLabel', () => {
  const cases: [string, string][] = [
    ['exact-url', 'Один и тот же адрес'],
    ['similar-title', 'Похожие заголовки'],
    ['whatever', 'whatever'],
  ]
  it.each(cases)('%s → %s', (kind, want) => {
    expect(duplicateKindLabel(kind)).toBe(want)
  })
})

describe('conflictingIds', () => {
  // Запись #481 стоит и в outdated, и в canonical: движок предлагает пометить
  // её устаревшей и канонической одновременно. Это противоречие в данных, и
  // страница обязана его показать, а не выдать два независимых совета.
  it('находит записи, попавшие больше чем в один раздел', () => {
    const audits: Audits = {
      outdated: [finding(481, ['verdict:skip-unavailable']), finding(58, [])],
      canonical: [finding(481, ['referenced by 4 entries'])],
      supersession: null,
    }
    expect(conflictingIds(audits)).toEqual([481])
  })

  it('без пересечений возвращает пустой список', () => {
    const audits: Audits = {
      outdated: [finding(1, [])],
      canonical: [finding(2, [])],
      supersession: null,
    }
    expect(conflictingIds(audits)).toEqual([])
  })
})

describe('findingCount', () => {
  // Счётчик во вкладке: три раздела аудита плюс группы дублей. Он показывает
  // объём работы, поэтому считает находки, а не уникальные записи.
  it('складывает все разделы аудита и группы дублей', () => {
    const audits: Audits = {
      outdated: [finding(1, []), finding(2, [])],
      canonical: [finding(3, [])],
      supersession: null,
    }
    const dups: DuplicateGroup[] = [{ Kind: 'exact-url', Key: 'u', EntryIDs: [4, 5] }]
    expect(findingCount(audits, dups)).toBe(4)
  })

  it('пустые данные дают ноль', () => {
    expect(findingCount({ outdated: null, canonical: null, supersession: null }, [])).toBe(0)
  })
})
