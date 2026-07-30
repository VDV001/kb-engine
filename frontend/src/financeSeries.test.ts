import { describe, expect, it } from 'vitest'
import { dayBars, monthBarLabel, monthBars } from './financeSeries'
import type { PeriodTotal } from './api'

const p = (period: string, total: string): PeriodTotal => ({ period, total, count: 1 })

describe('monthBarLabel', () => {
  const cases: [string, string][] = [
    ['2026-01', 'янв 26'],
    ['2026-07', 'июл 26'],
    ['2026-12', 'дек 26'],
    ['2025-03', 'мар 25'],
  ]
  for (const [period, want] of cases) {
    it(`${period} → ${want}`, () => {
      expect(monthBarLabel(period)).toBe(want)
    })
  }
})

describe('monthBars', () => {
  it('пустой ряд остаётся пустым', () => {
    expect(monthBars([], '2026-07-30')).toEqual([])
  })

  it('восстанавливает пропущенные месяцы нулями', () => {
    // Между мартом и июнем расходов не было. Без восстановления они встали бы
    // соседними столбцами, и полугодовой перерыв выглядел бы как его отсутствие.
    const bars = monthBars([p('2026-03', '100.00'), p('2026-06', '50.00')], '2026-07-30')
    expect(bars.map((b) => b.key)).toEqual(['2026-03', '2026-04', '2026-05', '2026-06'])
    expect(bars.map((b) => b.kopecks)).toEqual([10000, 0, 0, 5000])
  })

  it('переходит через границу года', () => {
    const bars = monthBars([p('2025-11', '10.00'), p('2026-02', '20.00')], '2026-07-30')
    expect(bars.map((b) => b.key)).toEqual(['2025-11', '2025-12', '2026-01', '2026-02'])
  })

  it('помечает текущий месяц и только его', () => {
    const bars = monthBars([p('2026-05', '1.00'), p('2026-07', '2.00')], '2026-07-30')
    expect(bars.filter((b) => b.current).map((b) => b.key)).toEqual(['2026-07'])
  })

  it('текущего месяца может не быть в ряду — тогда не помечен никто', () => {
    const bars = monthBars([p('2026-01', '1.00'), p('2026-02', '2.00')], '2026-07-30')
    expect(bars.some((b) => b.current)).toBe(false)
  })

  it('один месяц даёт один столбец', () => {
    expect(monthBars([p('2026-07', '5.00')], '2026-07-30').map((b) => b.key)).toEqual(['2026-07'])
  })
})

describe('dayBars', () => {
  it('окно всегда нужной длины, даже когда данных нет', () => {
    expect(dayBars([], '2026-07-30')).toHaveLength(31)
    expect(dayBars([], '2026-07-30', 7)).toHaveLength(7)
  })

  it('последний столбец — сегодня, первый — на days-1 раньше', () => {
    const bars = dayBars([], '2026-07-30', 7)
    expect(bars[0].key).toBe('2026-07-24')
    expect(bars[bars.length - 1].key).toBe('2026-07-30')
    expect(bars[bars.length - 1].current).toBe(true)
  })

  it('переходит через границу месяца назад', () => {
    const bars = dayBars([], '2026-03-02', 4)
    expect(bars.map((b) => b.key)).toEqual(['2026-02-27', '2026-02-28', '2026-03-01', '2026-03-02'])
  })

  it('раскладывает суммы по своим дням, остальные нули', () => {
    const bars = dayBars([p('2026-07-28', '12.34'), p('2026-07-30', '1.00')], '2026-07-30', 4)
    expect(bars.map((b) => [b.key, b.kopecks])).toEqual([
      ['2026-07-27', 0],
      ['2026-07-28', 1234],
      ['2026-07-29', 0],
      ['2026-07-30', 100],
    ])
  })

  it('дни вне окна не попадают в ряд', () => {
    // Сервер отдаёт ВСЕ дни с расходами, окно режет график — проверяем, что
    // старая запись не всплывает и не сдвигает шкалу.
    const bars = dayBars([p('2020-01-01', '999.00'), p('2026-07-30', '1.00')], '2026-07-30', 3)
    expect(bars).toHaveLength(3)
    expect(bars.some((b) => b.key === '2020-01-01')).toBe(false)
    expect(bars.reduce((n, b) => n + b.kopecks, 0)).toBe(100)
  })
})
