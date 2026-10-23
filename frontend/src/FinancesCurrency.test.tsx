// @vitest-environment jsdom
import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { FinancesView } from './FinancesView'

// Сводка, где есть траты не в рублях. Суммы выдуманы.
const summary = {
  expenseCount: 1,
  expenses: '100.00',
  incomeCount: 0,
  income: '0.00',
  net: '-100.00',
  excludedTransferCount: 0,
  excludedTransfers: '0.00',
  otherCurrencies: [
    { currency: 'USD', expenseCount: 2, expenses: '17.30', incomeCount: 1, income: '50.00', transferCount: 0, transfers: '0.00' },
  ],
  byCategory: [{ name: 'Еда', total: '100.00', count: 1 }],
  byAccount: [],
  byPlace: [],
  bySource: [],
  incomeBySource: [],
  bySubcategory: [],
  byMonth: [],
  byDay: [],
}

vi.mock('./api', () => ({
  api: {
    engine: async () => ({ sources: [{ flag: 'ledger', connected: true }] }),
    financeSummary: async () => summary,
  },
}))

afterEach(cleanup)

const finances = {
  accounts: [],
  transactions: [
    { id: '01R', kind: 'expense' as const, date: '2026-07-01', amount: '100.00', category: 'Еда' },
    { id: '01U', kind: 'expense' as const, date: '2026-07-02', amount: '17.30', category: 'Сервисы', currency: 'USD' },
  ],
}

// Доллары не складываются с рублями, поэтому витрина называет их отдельно и
// печатает каждую сумму в её единице. Без этого 17.30 USD в журнале читалось
// бы рублями, а итог не сходился бы с суммой строк без объяснения.
describe('FinancesView — записи не в рублях', () => {
  it('сумма в журнале печатается в своей валюте', async () => {
    render(<FinancesView finances={finances} masked={false} />)
    expect(await screen.findByText(/17,30 \$/)).toBeTruthy()
    expect(screen.queryByText(/17,30 ₽/)).toBeNull()
  })

  it('рядом с итогами названо, что не в рублях', async () => {
    render(<FinancesView finances={finances} masked={false} />)
    // Две строки: под расходами и под доходами — у USD есть и то, и другое.
    expect((await screen.findAllByText(/не в рублях/i)).length).toBe(2)
    expect(screen.getAllByText(/17,30 \$/).length).toBeGreaterThan(0)
    expect(screen.getAllByText(/50,00 \$/).length).toBeGreaterThan(0)
  })
})
