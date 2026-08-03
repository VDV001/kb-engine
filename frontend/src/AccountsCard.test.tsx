// @vitest-environment jsdom
import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'
import { AccountsCard } from './AccountsCard'
import type { Account } from './api'

afterEach(cleanup)

const accounts: Account[] = [
  { bank: 'Сбербанк', balance: '1234.50', updated: '2026-08-03' },
  { bank: 'Альфа-Банк', balance: '600.00', updated: '2026-07-31' },
  { bank: 'Заморозка → Вклад', balance: '0', updated: '2026-05-20' },
]

// Разбивка по счетам жила в терминале и не жила в вебе: страница показывала
// один общий итог, поэтому «сколько где лежит» приходилось смотреть в старом
// дашборде. Карточка закрывает ровно это расхождение поверхностей.
describe('AccountsCard — где лежат деньги', () => {
  it('показывает итог и каждый счёт отдельной строкой', () => {
    render(<AccountsCard accounts={accounts} expenses="100.00" income="50.00" today="2026-08-03" />)

    expect(screen.getByText('Сбербанк')).toBeDefined()
    expect(screen.getByText('Альфа-Банк')).toBeDefined()
    expect(screen.getByText('Заморозка → Вклад')).toBeDefined()
    // Итог — сумма счетов, а не одно из слагаемых.
    expect(screen.getByTestId('accounts-total').textContent).toContain('1 834')
  })

  it('показывает расходы и доходы периода', () => {
    render(<AccountsCard accounts={accounts} expenses="100.00" income="50.00" today="2026-08-03" />)

    expect(screen.getByTestId('accounts-expenses').textContent).toContain('100')
    expect(screen.getByTestId('accounts-income').textContent).toContain('50')
  })

  // Главный вопрос владельца к этой карточке был не «сколько», а «почему
  // число не сошлось с приложением банка». Ответ — в дате: остаток не
  // вычисляется из трат, он подтверждается глазами, и подтверждение стареет.
  it('называет дату подтверждения у каждого счёта', () => {
    render(<AccountsCard accounts={accounts} expenses="0" income="0" today="2026-08-03" />)

    expect(screen.getByTestId('confirmed-Сбербанк').textContent).toContain('03.08')
    expect(screen.getByTestId('confirmed-Альфа-Банк').textContent).toContain('31.07')
  })

  it('помечает подтверждение, устаревшее больше чем на две недели', () => {
    render(<AccountsCard accounts={accounts} expenses="0" income="0" today="2026-08-03" />)

    // 20.05 — два с половиной месяца назад, число давно могло уехать.
    expect(screen.getByTestId('stale-Заморозка → Вклад')).toBeDefined()
    // Сегодняшнее и трёхдневное подтверждения меткой не помечаются: иначе
    // пометка стоит на всех строках сразу и перестаёт что-либо значить.
    expect(screen.queryByTestId('stale-Сбербанк')).toBeNull()
    expect(screen.queryByTestId('stale-Альфа-Банк')).toBeNull()
  })

  // Пустой список — это «леджер подключён, а книга счетов нет». Молчать нельзя:
  // пустая карточка читается как «денег ноль», а не как «данных нет».
  it('без счетов говорит об этом словами, а не пустотой', () => {
    render(<AccountsCard accounts={[]} expenses="0" income="0" today="2026-08-03" />)

    expect(screen.getByText(/Счета не подключены/)).toBeDefined()
  })
})
