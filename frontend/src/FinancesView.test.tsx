// @vitest-environment jsdom
import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { FinancesView } from './FinancesView'

const engine = vi.hoisted(() => ({
  value: {
    version: '0.9.0',
    commit: 'abc1234',
    built: '2026-08-12T00:00:00Z',
    sources: [{ flag: 'ledger', connected: false }],
  } as unknown,
}))

const emptySummary = {
  expenseCount: 0,
  expenses: '0.00',
  incomeCount: 0,
  income: '0.00',
  net: '0.00',
  byCategory: [],
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
    engine: async () => engine.value,
    financeSummary: async () => emptySummary,
  },
}))

afterEach(cleanup)

// «Данных нет» и «данные не читаются» — разные ответы, и до этого они
// сливались в один экран с ложной инструкцией: владелец читал «запустите с
// --ledger и --from» при обоих уже переданных флагах и шёл добавлять то, что
// стоит. Причина (одна негодная строка журнала) видна только в терминале, где
// висит serve.
describe('FinancesView — пустой экран объясняет причину', () => {
  it('журнал не прочитан: не советует флаги, отсылает к терминалу', async () => {
    engine.value = { sources: [{ flag: 'ledger', connected: true }] }
    render(<FinancesView finances={null} masked={false} />)

    expect(await screen.findByText(/не прочитан|не читается/i)).toBeTruthy()
    expect(screen.queryByText(/запустите serve с флагами/i)).toBeNull()
  })

  it('леджер не подключён: называет флаги', async () => {
    engine.value = { sources: [{ flag: 'ledger', connected: false }] }
    render(<FinancesView finances={{ transactions: [], accounts: [] }} masked={false} />)

    expect(await screen.findByText(/--ledger/)).toBeTruthy()
  })

  // Третье состояние, которого прежде не существовало: файл передан, прочитан
  // и пуст. Совет добавить флаги здесь так же неверен, как и при отказе.
  it('леджер подключён и пуст: не советует флаги', async () => {
    engine.value = { sources: [{ flag: 'ledger', connected: true }] }
    render(<FinancesView finances={{ transactions: [], accounts: [] }} masked={false} />)

    expect(await screen.findByText(/пока пуст/i)).toBeTruthy()
    expect(screen.queryByText(/--ledger/)).toBeNull()
  })

  // Старая сборка сервера не сообщает о своих источниках. Выдуманная причина
  // хуже отсутствия причины, поэтому здесь остаётся прежний текст.
  it('сборка о источниках не сообщает: прежний текст', async () => {
    engine.value = { version: '0.9.0' }
    render(<FinancesView finances={{ transactions: [], accounts: [] }} masked={false} />)

    expect(await screen.findByText(/--ledger/)).toBeTruthy()
  })
})
