import { describe, expect, it } from 'vitest'
import type { Entry } from './api'
import { staleCategories, weeklyGrowth } from './analyticsSidebar'

const e = (over: Partial<Entry>): Entry => ({
  id: 1,
  title: 'x',
  category: 'meta',
  kind: 'article',
  lifecycle: 'active',
  ...over,
})

// «Сегодня» передаётся, а не берётся из часов: тест, зависящий от текущей даты,
// начинает падать сам по себе через неделю после написания.
const today = new Date('2026-07-31')

describe('staleCategories', () => {
  it('сортирует по давности последнего пополнения', () => {
    const got = staleCategories(
      [
        e({ id: 1, category: 'design-tools', date_added: '2026-03-27' }),
        e({ id: 2, category: 'golang', date_added: '2026-07-30' }),
        e({ id: 3, category: 'nodejs', date_added: '2026-04-03' }),
        e({ id: 4, category: 'golang', date_added: '2026-07-01' }),
      ],
      today,
      2,
    )
    expect(got.map((c) => c.category)).toEqual(['design-tools', 'nodejs'])
    expect(got[0].weeks).toBe(18)
    // Счётчик — размер категории целиком, а не число старых записей: вопрос
    // «сколько знания стынет», а не «сколько записей просрочено».
    expect(got[0].count).toBe(1)
  })

  it('категория без дат не выдаёт себя за свежую', () => {
    const got = staleCategories([e({ category: 'meta' })], today, 5)
    expect(got).toHaveLength(0)
  })
})

describe('weeklyGrowth', () => {
  it('складывает записи по неделям и считает итог', () => {
    const got = weeklyGrowth(
      [
        e({ id: 1, date_added: '2026-07-30' }),
        e({ id: 2, date_added: '2026-07-29' }),
        e({ id: 3, date_added: '2026-07-20' }),
      ],
      today,
      2,
    )
    expect(got.weeks).toHaveLength(2)
    expect(got.total).toBe(3)
    // Последняя неделя — правый край: 29 и 30 июля попадают в неё вдвоём.
    expect(got.weeks[got.weeks.length - 1]).toBe(2)
  })
})
