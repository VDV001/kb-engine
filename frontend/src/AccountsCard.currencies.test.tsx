// @vitest-environment jsdom
import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'
import { AccountsCard } from './AccountsCard'
import type { Account, BalanceTotal, CurrencyBalance } from './api'

afterEach(cleanup)

const spaced = (el: HTMLElement) => (el.textContent ?? '').replace(/[\u00a0\u202f]/g, ' ')

// Рубли и доллары не складываются и не пересчитываются по курсу (решение
// владельца 05.11): главная строка — деньги по каждой валюте, как их отдает
// движок; рублевая оценка по курсу счетов — мелко рядом, как справка.
const accounts: Account[] = [
  { bank: 'Сбербанк', balance: '1000.00', updated: '2026-11-05' },
  { bank: 'Wise', balance: '100.00', updated: '2026-11-05', currency: 'USD', rate: '90.00', base_value: '9000.00' },
  { bank: 'Резерв → Вклад', balance: '5000.00', updated: '2026-11-05' },
]
const currencies: CurrencyBalance[] = [
  { currency: '', current: '6000.00', free: '1000.00' },
  { currency: 'USD', current: '100.00', free: '100.00' },
]
const totals: BalanceTotal[] = [
  { group: '', currency: '', confirmed: '1000.00', current: '1000.00' },
  { group: '', currency: 'USD', confirmed: '100.00', current: '100.00' },
  { group: 'Резерв', currency: '', confirmed: '5000.00', current: '5000.00' },
]

describe('AccountsCard — деньги по валютам', () => {
  it('главная строка — каждая валюта отдельно, без пересчета', () => {
    render(
      <AccountsCard accounts={accounts} currencies={currencies} totals={totals} free="10000.00"
        expenses="0" income="0" today="2026-11-05" />,
    )
    const total = spaced(screen.getByTestId('accounts-total'))
    expect(total).toContain('6 000')
    expect(total).toContain('100')
    expect(total).toContain('$')
    // Сумма по курсу в главной строке не стоит: 6 000 + 100 · 90 = 15 000.
    expect(total).not.toContain('15 000')
  })

  it('оценку в рублях по курсу счетов показывает мелко и называет оценкой', () => {
    render(
      <AccountsCard accounts={accounts} currencies={currencies} totals={totals}
        expenses="0" income="0" today="2026-11-05" />,
    )
    const est = spaced(screen.getByTestId('accounts-estimate'))
    expect(est).toContain('≈')
    expect(est).toContain('15 000')
    expect(est).toContain('по курсу')
  })

  it('свободные деньги — тоже по валютам', () => {
    render(
      <AccountsCard accounts={accounts} currencies={currencies} totals={totals} free="10000.00"
        expenses="0" income="0" today="2026-11-05" />,
    )
    const free = spaced(screen.getByTestId('accounts-free'))
    expect(free).toContain('1 000')
    expect(free).toContain('$')
    expect(free).not.toContain('10 000')
  })

  it('итог рода — по валютам рода, как отдал движок', () => {
    render(
      <AccountsCard accounts={accounts} currencies={currencies} totals={totals}
        expenses="0" income="0" today="2026-11-05" />,
    )
    const banks = spaced(screen.getByTestId('group-total-Банки'))
    expect(banks).toContain('1 000')
    expect(banks).toContain('$')
    expect(spaced(screen.getByTestId('group-total-Резерв'))).toContain('5 000')
  })

  it('у книги в одной валюте оценки рядом нет: объяснять нечего', () => {
    render(
      <AccountsCard
        accounts={[accounts[0]]}
        currencies={[{ currency: '', current: '1000.00', free: '1000.00' }]}
        totals={[totals[0]]}
        expenses="0" income="0" today="2026-11-05"
      />,
    )
    expect(spaced(screen.getByTestId('accounts-total'))).toContain('1 000')
    expect(screen.queryByTestId('accounts-estimate')).toBeNull()
  })
})
