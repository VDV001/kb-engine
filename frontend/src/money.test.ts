import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'

import type { Transaction } from './api'
import {
  daysOfMonth,
  formatRub,
  monthLabel,
  monthOf,
  monthsBetween,
  sumBy,
  sumByAccount,
  toKopecks,
  toRoubleBars,
} from './money'

describe('toKopecks', () => {
  // Every one of these is a way a naive parse goes wrong on real ledger data.
  it.each([
    ['500.00', 50000],
    ['89.99', 8999],
    ['0.01', 1],
    ['-5500.00', -550000],
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
    [-550000, '-5 500,00 ₽'],
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

describe('daysOfMonth', () => {
  it.each([
    ['2026-02', 28],
    ['2024-02', 29], // leap year
    ['2026-04', 30],
    ['2026-07', 31],
  ])('%s has %d days', (month, count) => {
    const days = daysOfMonth(month)
    expect(days).toHaveLength(count)
    expect(days[0]).toBe(`${month}-01`)
  })
})

describe('monthsBetween', () => {
  it('keeps a month with no spending as a gap', () => {
    expect(monthsBetween('2026-03', '2026-07')).toEqual([
      '2026-03', '2026-04', '2026-05', '2026-06', '2026-07',
    ])
  })

  it('crosses a year boundary', () => {
    expect(monthsBetween('2025-11', '2026-02')).toEqual(['2025-11', '2025-12', '2026-01', '2026-02'])
  })

  it('is empty when there is nothing to span', () => {
    expect(monthsBetween('', '')).toEqual([])
  })
})

describe('toRoubleBars', () => {
  it('orders biggest first with the name breaking a draw, like the CLI', () => {
    const bars = toRoubleBars({ 'Б': 10000, 'А': 10000, 'В': 50000 })
    expect(Object.keys(bars)).toEqual(['В', 'А', 'Б'])
  })
})

// The golden case is shared with Go: internal/usecase/finance/golden_test.go
// reads this same file. The dashboard totals its own figures instead of taking
// a summary from the server, so the arithmetic exists twice — this is what
// keeps the two copies from drifting apart unnoticed.
interface GoldenTotal {
  category: string
  total: string
  count: number
}

interface Golden {
  transactions: Transaction[]
  expected: {
    expenseCount: number
    expenses: string
    incomeCount: number
    income: string
    net: string
    byCategory: GoldenTotal[]
    byAccount: GoldenTotal[]
  }
}

describe('golden case shared with the CLI', () => {
  const golden = JSON.parse(
    readFileSync(new URL('../../testdata/finance-golden.json', import.meta.url), 'utf8'),
  ) as Golden
  const txs = golden.transactions
  const expenses = txs.filter((t) => t.kind === 'expense')
  const income = txs.filter((t) => t.kind === 'income')

  const decimal = (kopecks: number) =>
    `${kopecks < 0 ? '-' : ''}${Math.floor(Math.abs(kopecks) / 100)}.${String(Math.abs(kopecks) % 100).padStart(2, '0')}`

  it('totals expenses and income exactly as Summarize does', () => {
    const spent = expenses.reduce((n, t) => n + toKopecks(t.amount), 0)
    const earned = income.reduce((n, t) => n + toKopecks(t.amount), 0)
    expect(expenses).toHaveLength(golden.expected.expenseCount)
    expect(income).toHaveLength(golden.expected.incomeCount)
    expect(decimal(spent)).toBe(golden.expected.expenses)
    expect(decimal(earned)).toBe(golden.expected.income)
    expect(decimal(earned - spent)).toBe(golden.expected.net)
  })

  it('breaks down by category exactly as Summarize does', () => {
    const byCategory = sumBy(expenses, (t) => t.category ?? '')
    const want = Object.fromEntries(
      golden.expected.byCategory.map((r) => [r.category, r.total]),
    )
    expect(Object.fromEntries(Object.entries(byCategory).map(([k, v]) => [k, decimal(v)]))).toEqual(want)
  })

  it('breaks down by account exactly as Summarize does, leaving out rows with none', () => {
    const byAccount = sumByAccount(expenses)
    const want = Object.fromEntries(
      golden.expected.byAccount.map((r) => [r.category, r.total]),
    )
    expect(Object.fromEntries(Object.entries(byAccount).map(([k, v]) => [k, decimal(v)]))).toEqual(want)
    // One row in the set has no account at all; it must not appear as a blank.
    expect(Object.keys(byAccount)).not.toContain('')
  })
})
