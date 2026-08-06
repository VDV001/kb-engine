import { describe, expect, it } from 'vitest'

import { formatRub, monthLabel, monthOf, todayLocal, toKopecks } from './money'

describe('toKopecks', () => {
  // Every one of these is a way a naive parse goes wrong on real ledger data.
  it.each([
    ['500.00', 50000],
    ['89.99', 8999],
    ['0.01', 1],
    ['-4321.00', -432100],
    ['-0.50', -50],
    ['07.08', 708], // leading zero must not be read as octal
    ['1.5', 150], // one decimal place pads, not truncates
    ['1000', 100000], // no decimal part at all
  ])('parses %s', (input, want) => {
    expect(toKopecks(input)).toBe(want)
  })

  it('never goes through a float', () => {
    // 89.99 as a float is 89.98999999999999; summing kopecks keeps it exact.
    const total = ['89.99', '89.99', '89.99'].reduce((n, a) => n + toKopecks(a), 0)
    expect(total).toBe(26997)
    expect(formatRub(total)).toBe('269,97 ₽')
  })
})

describe('formatRub', () => {
  it.each([
    [50000, '500,00 ₽'],
    [1, '0,01 ₽'],
    [-432100, '-4 321,00 ₽'],
    [0, '0,00 ₽'],
  ])('formats %d kopecks', (input, want) => {
    expect(formatRub(input).replace(/ /g, ' ')).toBe(want)
  })
})

describe('monthOf', () => {
  it('takes the prefix rather than parsing a date', () => {
    // A parsed Date would apply the browser's timezone, and a purchase made on
    // the 1st would slide into the previous month west of UTC.
    expect(monthOf('2026-07-01')).toBe('2026-07')
    expect(monthOf('2026-12-31')).toBe('2026-12')
  })
})

describe('monthLabel', () => {
  it.each([
    ['2026-01', 'январь 2026'],
    ['2026-07', 'июль 2026'],
    ['2026-12', 'декабрь 2026'],
  ])('names %s', (input, want) => {
    expect(monthLabel(input)).toBe(want)
  })
})


describe('todayLocal', () => {
  // Дата «сегодня» берётся по местному времени, а не по UTC. Владелец работает
  // ночами: между полуночью и пятью утра по книге UTC-дата ещё вчерашняя, и
  // витрина показывала вчерашний день — и в баре недели, и в возрасте
  // подтверждения баланса.
  it('в ночные часы называет местный день, а не вчерашний по UTC', () => {
    // 06.08 01:00 по книге (UTC+5) = 05.08 20:00 UTC
    const night = new Date('2026-08-05T20:00:00Z')
    expect(night.toISOString().slice(0, 10)).toBe('2026-08-05') // как было
    expect(todayLocal(night)).toBe('2026-08-06') // как надо
  })

  it('днём совпадает с UTC-датой', () => {
    expect(todayLocal(new Date('2026-08-06T09:00:00Z'))).toBe('2026-08-06')
  })
})
