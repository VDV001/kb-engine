import { describe, expect, it } from 'vitest'
import { layoutFlow } from './flowLayout'
import type { DocCard } from './api'

/** Карточка-ребро в том виде, в каком её пишет владелец в team.json. */
function edge(from: string, to: string, extra: Partial<DocCard> = {}): DocCard {
  return { title: `${from} → ${to}`, from, to, ...extra }
}

describe('layoutFlow', () => {
  it('узлы выводятся из рёбер, каждый по одному разу', () => {
    const flow = layoutFlow([edge('Генеральный', 'Даниил'), edge('Даниил', 'Разработчики')])

    expect(flow.nodes.map((n) => n.id)).toEqual(['Генеральный', 'Даниил', 'Разработчики'])
  })

  // Ярус — не порядок карточек в файле, а расстояние от входа. Иначе схема
  // просто повторяла бы список, ради чего рисовать её незачем.
  it('ярус считается по задачам, а не по порядку карточек', () => {
    const flow = layoutFlow([edge('Даниил', 'Разработчики'), edge('Генеральный', 'Даниил')])

    const tier = (id: string) => flow.nodes.find((n) => n.id === id)!.tier
    expect(tier('Генеральный')).toBe(0)
    expect(tier('Даниил')).toBe(1)
    expect(tier('Разработчики')).toBe(2)
  })

  // «Снизу вверх — статусы и эскалация»: обратное ребро не должно опускать
  // отправителя ниже получателя, иначе пара «задача вниз / статус вверх»
  // растянула бы двух участников на четыре яруса.
  it('статус не влияет на ярусы', () => {
    const flow = layoutFlow([
      edge('Генеральный', 'Даниил'),
      edge('Даниил', 'Генеральный', { kind: 'status' }),
    ])

    expect(flow.nodes.find((n) => n.id === 'Даниил')!.tier).toBe(1)
    expect(flow.edges.filter((e) => e.kind === 'status')).toHaveLength(1)
  })

  // «Заказчик → Данил → Даниил»: требования идут ЧЕРЕЗ владельца проектов.
  // Ребро напрямую от заказчика к лиду сказало бы неправду о том, кто режет
  // их в бэклог.
  it('via даёт два ребра и промежуточный узел', () => {
    const flow = layoutFlow([edge('Заказчик', 'Даниил', { via: 'Данил' })])

    expect(flow.nodes.map((n) => n.id)).toEqual(['Заказчик', 'Данил', 'Даниил'])
    expect(flow.edges.map((e) => [e.from, e.to])).toEqual([
      ['Заказчик', 'Данил'],
      ['Данил', 'Даниил'],
    ])
  })

  it('карточки без from/to не попадают в схему', () => {
    const flow = layoutFlow([{ title: 'Просто описание', body: 'без связей' }, edge('А', 'Б')])

    expect(flow.nodes.map((n) => n.id)).toEqual(['А', 'Б'])
    expect(flow.edges).toHaveLength(1)
  })

  it('карточка ведёт к своему ребру, чтобы клик открывал описание', () => {
    const card = edge('Генеральный', 'Даниил', { body: 'приоритеты одним потоком' })
    const flow = layoutFlow([card])

    expect(flow.edges[0].card).toBe(card)
  })

  // Цикл в задачах — ошибка в данных, но она не должна вешать вкладку:
  // расчёт ярусов идёт по длиннейшему пути и без остановки крутился бы вечно.
  it('цикл не вешает раскладку', () => {
    const flow = layoutFlow([edge('А', 'Б'), edge('Б', 'В'), edge('В', 'А')])

    expect(flow.nodes).toHaveLength(3)
    expect(flow.nodes.every((n) => Number.isFinite(n.tier))).toBe(true)
  })

  it('у каждого узла есть координаты внутри холста', () => {
    const flow = layoutFlow([edge('Генеральный', 'Даниил'), edge('Даниил', 'Разработчики')])

    expect(flow.width).toBeGreaterThan(0)
    expect(flow.height).toBeGreaterThan(0)
    for (const n of flow.nodes) {
      expect(n.x).toBeGreaterThanOrEqual(0)
      expect(n.x + n.width).toBeLessThanOrEqual(flow.width)
      expect(n.y).toBeGreaterThanOrEqual(0)
    }
  })

  it('пустой список даёт пустую схему, а не падение', () => {
    const flow = layoutFlow([])

    expect(flow.nodes).toHaveLength(0)
    expect(flow.edges).toHaveLength(0)
  })
})
