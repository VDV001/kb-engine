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

  it('оценка называет валюту, которую оценить нечем, а не прячет ее', () => {
    const withSum: Account[] = [
      ...accounts,
      { bank: 'Kapital UZS', balance: '1333000.00', updated: '2026-11-05', currency: 'UZS', unvalued: true },
    ]
    render(
      <AccountsCard
        accounts={withSum}
        currencies={[...currencies, { currency: 'UZS', current: '1333000.00', free: '1333000.00' }]}
        totals={totals}
        expenses="0" income="0" today="2026-11-05"
      />,
    )
    const est = spaced(screen.getByTestId('accounts-estimate'))
    expect(est).toContain('15 000')
    expect(est).toContain('без сум')
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

describe('AccountsCard — по валютам, края', () => {
  it('сервер без счетов (currencies: []) — итог «0,00 ₽», а не пустота', () => {
    render(<AccountsCard accounts={[]} currencies={[]} totals={[]} expenses="0" income="0" today="2026-11-05" />)
    expect(spaced(screen.getByTestId('accounts-total'))).toContain('0,00')
  })

  it('валюта по нулю не печатается и не включает оценку', () => {
    render(
      <AccountsCard
        accounts={[accounts[0], { bank: 'Wise', balance: '0.00', updated: '2026-11-05', currency: 'USD', rate: '90.00', base_value: '0.00' }]}
        currencies={[{ currency: '', current: '1000.00', free: '1000.00' }, { currency: 'USD', current: '0.00', free: '0.00' }]}
        totals={[totals[0], { group: '', currency: 'USD', confirmed: '0.00', current: '0.00' }]}
        expenses="0" income="0" today="2026-11-05"
      />,
    )
    expect(spaced(screen.getByTestId('accounts-total'))).not.toContain('$')
    expect(screen.queryByTestId('accounts-estimate')).toBeNull()
    expect(spaced(screen.getByTestId('group-total-Банки'))).not.toContain('$')
  })

  it('ничего свободного — ноль, а не рублевая оценка по курсу', () => {
    render(
      <AccountsCard
        accounts={[{ bank: 'Резерв → Наличные', balance: '50.00', updated: '2026-11-05', currency: 'USD', rate: '90.00', base_value: '4500.00' }, accounts[2]]}
        currencies={[{ currency: 'USD', current: '50.00', free: '0.00' }, { currency: '', current: '5000.00', free: '0.00' }]}
        totals={[{ group: 'Резерв', currency: 'USD', confirmed: '50.00', current: '50.00' }, totals[2]]}
        free="4500.00"
        expenses="0" income="0" today="2026-11-05"
      />,
    )
    const free = spaced(screen.getByTestId('accounts-free'))
    expect(free).toContain('0,00')
    expect(free).not.toContain('4 500')
  })

  it('свободное в валюте, где итог ноль, не теряется', () => {
    // Обычный счет 0, отложено +100, обязательство −100: итог 0, свободно −100.
    render(
      <AccountsCard
        accounts={accounts}
        currencies={[{ currency: '', current: '1000.00', free: '1000.00' }, { currency: 'USD', current: '0.00', free: '-100.00' }]}
        totals={totals}
        expenses="0" income="0" today="2026-11-05"
      />,
    )
    expect(spaced(screen.getByTestId('accounts-free'))).toContain('-100,00 $')
  })

  it('свободно — без валют, где свободного ноль', () => {
    render(
      <AccountsCard
        accounts={[accounts[0], { bank: 'Резерв → Наличные', balance: '50.00', updated: '2026-11-05', currency: 'USD', rate: '90.00', base_value: '4500.00' }]}
        currencies={[{ currency: '', current: '1000.00', free: '1000.00' }, { currency: 'USD', current: '50.00', free: '0.00' }]}
        totals={[totals[0], { group: 'Резерв', currency: 'USD', confirmed: '50.00', current: '50.00' }]}
        expenses="0" income="0" today="2026-11-05"
      />,
    )
    expect(spaced(screen.getByTestId('accounts-free'))).not.toContain('$')
  })

  it('свободно и итог рода — тоже столбиком, без «·»: в узкой карточке «·» переносится посреди суммы', () => {
    render(
      <AccountsCard accounts={accounts} currencies={currencies} totals={totals}
        expenses="0" income="0" today="2026-11-05" />,
    )
    expect(spaced(screen.getByTestId('accounts-free'))).not.toContain('·')
    expect(spaced(screen.getByTestId('group-total-Банки'))).not.toContain('·')
  })

  it('род, которого нет в totals движка, показывает прежний итог, а не пустоту', () => {
    render(
      <AccountsCard accounts={accounts} currencies={currencies} totals={[totals[0], totals[1]]}
        expenses="0" income="0" today="2026-11-05" />,
    )
    expect(spaced(screen.getByTestId('group-total-Резерв'))).toContain('5 000')
  })
})
