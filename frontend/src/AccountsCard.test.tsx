// @vitest-environment jsdom
import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'
import { AccountsCard } from './AccountsCard'
import type { Account } from './api'

afterEach(cleanup)

// Суммы печатаются с НЕразрывным пробелом между разрядами, а в исходнике теста
// стоит обычный. Сверять надо в той форме, в какой число читает человек, —
// иначе верный вывод выглядит ошибкой (и наоборот).
const spaced = (el: HTMLElement) => (el.textContent ?? '').replace(/ /g, ' ')

const accounts: Account[] = [
  { bank: 'Сбербанк', balance: '1234.50', updated: '2026-08-03' },
  { bank: 'Альфа-Банк', balance: '600.00', updated: '2026-07-31' },
  { bank: 'Резерв → Депозит', balance: '0', updated: '2026-05-20' },
]

// Разбивка по счетам жила в терминале и не жила в вебе: страница показывала
// один общий итог, поэтому «сколько где лежит» приходилось смотреть в старом
// дашборде. Карточка закрывает ровно это расхождение поверхностей.
describe('AccountsCard — где лежат деньги', () => {
  it('показывает итог и каждый счёт отдельной строкой', () => {
    render(<AccountsCard accounts={accounts} expenses="100.00" income="50.00" today="2026-08-03" />)

    expect(screen.getByText('Сбербанк')).toBeDefined()
    expect(screen.getByText('Альфа-Банк')).toBeDefined()
    // Счёт с родом в имени показан заголовком рода и коротким именем под ним:
    // строка «Резерв → Депозит» повторяла бы слово, которое уже написано
    // заголовком, и тратила бы на это ширину, которой у карточки нет.
    expect(screen.getByTestId('group-Резерв')).toBeDefined()
    expect(screen.getByText('Депозит')).toBeDefined()
    // Итог — сумма счетов, а не одно из слагаемых.
    expect(spaced(screen.getByTestId('accounts-total'))).toContain('1 834')
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
    expect(screen.getByTestId('stale-Резерв → Депозит')).toBeDefined()
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

// Итог и строка счёта показывают остаток на сейчас, а не подтверждённое число.
// Владелец записал трату и увидел прежний итог — экран выглядел сломанным,
// хотя данные были верны: он показывал вчерашний подтверждённый остаток.
describe('AccountsCard — остаток на сейчас', () => {
  const withSpending: Account[] = [
    { bank: 'Сбербанк', balance: '1000.00', updated: '2026-08-04', current: '977.00', spent: '23.00' },
  ]

  it('в итоге стоит остаток на сейчас, а не подтверждённый', () => {
    render(<AccountsCard accounts={withSpending} expenses="23.00" income="0" today="2026-08-04" />)

    expect(spaced(screen.getByTestId('accounts-total'))).toContain('977')
  })

  it('называет, сколько списано после подтверждения', () => {
    render(<AccountsCard accounts={withSpending} expenses="23.00" income="0" today="2026-08-04" />)

    expect(spaced(screen.getByTestId('confirmed-Сбербанк'))).toContain('23')
  })

  // Расчёт, ушедший в минус, — признак устаревшего подтверждения, а не долг:
  // доходы счёта не имеют, и движок их не видит.
  it('помечает счёт, у которого расчёт ушёл в минус', () => {
    const negative: Account[] = [
      { bank: 'Т-Банк', balance: '40.00', updated: '2026-08-04', current: '-557.00', spent: '597.00', needs_confirmation: true },
    ]
    render(<AccountsCard accounts={negative} expenses="0" income="0" today="2026-08-04" />)

    expect(screen.getByTestId('stale-Т-Банк')).toBeDefined()
  })
})

// Деньги на карте, деньги отложенные и деньги, которых сейчас нет, потому что
// их занял человек, — это не одна сумма. Пока карточка складывала их в одно
// число, оно отвечало не на тот вопрос, ради которого на него смотрят.
describe('AccountsCard — рода счетов', () => {
  const mixed: Account[] = [
    { bank: 'Сбербанк', balance: '1000.00', updated: '2026-08-04', current: '1000.00' },
    { bank: 'Резерв → Наличные', balance: '150000.00', updated: '2026-07-25', current: '150000.00' },
    { bank: 'Займ → Коллеге', balance: '3000.00', updated: '2026-08-04', current: '3000.00' },
  ]

  it('собирает счета одного рода под общим заголовком', () => {
    render(<AccountsCard accounts={mixed} expenses="0" income="0" today="2026-08-04" />)

    expect(screen.getByTestId('group-Резерв')).toBeDefined()
    expect(spaced(screen.getByTestId('group-total-Займ'))).toContain('3 000')
    // Внутри группы счёт назван коротким именем: слово «Займ» уже стоит
    // заголовком, и повторять его в каждой строке значит тратить ширину.
    expect(screen.getByText('Коллеге')).toBeDefined()
  })

  it('называет, сколько денег свободно, а не только общий итог', () => {
    render(<AccountsCard accounts={mixed} expenses="0" income="0" today="2026-08-04" />)

    // Общий итог остаётся общим: он про всё, чем владелец владеет.
    expect(spaced(screen.getByTestId('accounts-total'))).toContain('154 000')
    // А рядом — сколько из этого лежит на карте и доступно сейчас.
    expect(spaced(screen.getByTestId('accounts-free'))).toContain('1 000')
  })

  // Когда особых счетов нет, второй строки быть не должно: «свободно 1 000»
  // под итогом «1 000» — это шум, объясняющий то, чего не происходит.
  it('молчит про свободные деньги, когда все счета обычные', () => {
    const plain = accounts.filter((a) => !a.bank.includes('→'))
    render(<AccountsCard accounts={plain} expenses="0" income="0" today="2026-08-03" />)

    expect(screen.queryByTestId('accounts-free')).toBeNull()
  })
})
