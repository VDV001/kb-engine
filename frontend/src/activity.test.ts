import { describe, expect, it } from 'vitest'
import { buildActivity, DAY_LABELS } from './activity'
import type { Entry } from './api'

/** Минимальная запись: полю даты в тесте важно только оно само. */
function entry(id: number, dates: { added?: string; created?: string }): Entry {
  return {
    id,
    title: `E${id}`,
    category: 'golang',
    kind: 'article',
    lifecycle: 'active',
    date_added: dates.added,
    date_created: dates.created,
  } as Entry
}

const TODAY = new Date('2026-07-31T12:00:00Z') // пятница

describe('buildActivity', () => {
  it('строит сетку 7 строк на weeks+1 колонок и начинает с понедельника', () => {
    const a = buildActivity([], { weeks: 4, today: TODAY })

    expect(a.columns).toHaveLength(5)
    for (const col of a.columns) {
      expect(col.days).toHaveLength(7)
    }
    expect(DAY_LABELS).toHaveLength(7)
    // Первая ячейка первой колонки — понедельник.
    expect(new Date(`${a.columns[0].days[0].date}T00:00:00Z`).getUTCDay()).toBe(1)
  })

  it('кладёт запись в ячейку своей даты и считает совпадения', () => {
    const a = buildActivity(
      [
        entry(1, { added: '2026-07-29' }),
        entry(2, { added: '2026-07-29' }),
        entry(3, { added: '2026-07-30' }),
      ],
      { weeks: 4, today: TODAY },
    )

    const cells = a.columns.flatMap((c) => c.days)
    expect(cells.find((d) => d.date === '2026-07-29')?.count).toBe(2)
    expect(cells.find((d) => d.date === '2026-07-30')?.count).toBe(1)
    expect(a.maxCount).toBe(2)
  })

  // Расхождение с исходным дашбордом сделано намеренно: заголовок обещает
  // «записи добавленные по дням», а исходник считал по date_created, то есть по
  // дате публикации статьи — это другой факт.
  it('считает по дате добавления, а date_created берёт лишь как запасную', () => {
    const a = buildActivity(
      [
        entry(1, { added: '2026-07-30', created: '2026-05-01' }),
        entry(2, { created: '2026-07-29' }),
      ],
      { weeks: 4, today: TODAY },
    )

    const cells = a.columns.flatMap((c) => c.days)
    expect(cells.find((d) => d.date === '2026-07-30')?.count).toBe(1)
    expect(cells.find((d) => d.date === '2026-05-01')).toBeUndefined()
    expect(cells.find((d) => d.date === '2026-07-29')?.count).toBe(1)
  })

  it('помечает дни после сегодняшнего, чтобы будущее не закрашивалось', () => {
    const a = buildActivity([], { weeks: 1, today: TODAY })
    const cells = a.columns.flatMap((c) => c.days)

    expect(cells.find((d) => d.date === '2026-07-31')?.isFuture).toBe(false)
    expect(cells.find((d) => d.date === '2026-08-01')?.isFuture).toBe(true)
  })

  it('раскладывает уровни по четвертям от максимума', () => {
    const many = [
      ...Array.from({ length: 8 }, (_, i) => entry(100 + i, { added: '2026-07-30' })), // max = 8
      entry(1, { added: '2026-07-29' }), // 1/8 → 1
      ...Array.from({ length: 4 }, (_, i) => entry(200 + i, { added: '2026-07-28' })), // 4/8 → 2
      ...Array.from({ length: 6 }, (_, i) => entry(300 + i, { added: '2026-07-27' })), // 6/8 → 3
    ]
    const a = buildActivity(many, { weeks: 4, today: TODAY })
    const level = (date: string) => a.columns.flatMap((c) => c.days).find((d) => d.date === date)?.level

    expect(a.maxCount).toBe(8)
    expect(level('2026-07-26')).toBe(0) // пусто
    expect(level('2026-07-29')).toBe(1)
    expect(level('2026-07-28')).toBe(2)
    expect(level('2026-07-27')).toBe(3)
    expect(level('2026-07-30')).toBe(4)
  })

  it('не падает на записи без дат и на пустом каталоге', () => {
    const a = buildActivity([entry(1, {})], { weeks: 2, today: TODAY })
    expect(a.maxCount).toBe(0)
    expect(a.columns.flatMap((c) => c.days).every((d) => d.count === 0)).toBe(true)
  })
})
