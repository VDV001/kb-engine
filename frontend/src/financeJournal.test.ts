import { describe, expect, it } from 'vitest'
import { filterJournal, sortJournal } from './financeJournal'
import type { Transaction } from './api'

const tx = (over: Partial<Transaction> & { id: string; date: string; amount: string }): Transaction => ({
  kind: 'expense',
  ...over,
})

const rows: Transaction[] = [
  tx({ id: 'a', date: '2026-07-01', amount: '100.00', category: 'Еда', subcategory: 'Кафе', place: 'Поль', description: 'Завтрак' }),
  tx({ id: 'b', date: '2026-07-15', amount: '2500.50', category: 'Транспорт', place: 'Яндекс Go' }),
  tx({ id: 'c', date: '2026-06-20', amount: '89.99', category: 'Еда', subcategory: 'Продукты', place: 'Пятёрочка' }),
  tx({ id: 'd', date: '2026-08-02', amount: '9000.00', category: 'Жильё', kind: 'income', source: 'Зарплата' }),
]

describe('filterJournal', () => {
  it('без критериев отдаёт всё', () => {
    expect(filterJournal(rows, {}).map((r) => r.id)).toEqual(['a', 'b', 'c', 'd'])
  })

  it('фильтрует по набору месяцев', () => {
    expect(filterJournal(rows, { months: ['2026-07'] }).map((r) => r.id)).toEqual(['a', 'b'])
    expect(filterJournal(rows, { months: ['2026-06', '2026-08'] }).map((r) => r.id)).toEqual(['c', 'd'])
  })

  it('пустой набор месяцев не фильтрует — это «за всё время», а не «ничего»', () => {
    expect(filterJournal(rows, { months: [] })).toHaveLength(4)
  })

  it('фильтрует по категории', () => {
    expect(filterJournal(rows, { category: 'Еда' }).map((r) => r.id)).toEqual(['a', 'c'])
  })

  it('границы дат включительные', () => {
    expect(filterJournal(rows, { from: '2026-07-01', to: '2026-07-15' }).map((r) => r.id)).toEqual(['a', 'b'])
  })

  it('ищет по месту, описанию, категории и подкатегории', () => {
    expect(filterJournal(rows, { search: 'поль' }).map((r) => r.id)).toEqual(['a'])
    expect(filterJournal(rows, { search: 'завтрак' }).map((r) => r.id)).toEqual(['a'])
    expect(filterJournal(rows, { search: 'продукты' }).map((r) => r.id)).toEqual(['c'])
    expect(filterJournal(rows, { search: 'транспорт' }).map((r) => r.id)).toEqual(['b'])
  })

  it('поиск не зависит от регистра и от пробелов по краям', () => {
    expect(filterJournal(rows, { search: '  ПЯТЁРОЧКА  ' }).map((r) => r.id)).toEqual(['c'])
  })

  it('пустой поиск не фильтрует', () => {
    expect(filterJournal(rows, { search: '   ' })).toHaveLength(4)
  })

  it('критерии складываются, а не заменяют друг друга', () => {
    expect(filterJournal(rows, { months: ['2026-07'], category: 'Еда' }).map((r) => r.id)).toEqual(['a'])
  })

  it('не мутирует вход', () => {
    const copy = [...rows]
    filterJournal(rows, { category: 'Еда' })
    expect(rows).toEqual(copy)
  })
})

describe('sortJournal', () => {
  it('по дате убыв. — порядок по умолчанию', () => {
    expect(sortJournal(rows, 'date', 'desc').map((r) => r.id)).toEqual(['d', 'b', 'a', 'c'])
  })

  it('по дате возр.', () => {
    expect(sortJournal(rows, 'date', 'asc').map((r) => r.id)).toEqual(['c', 'a', 'b', 'd'])
  })

  it('по сумме сравнивает ЧИСЛА, а не строки', () => {
    // Строковое сравнение поставило бы «89.99» выше «2500.50»: это та ошибка,
    // ради которой суммы вообще переводятся в копейки.
    expect(sortJournal(rows, 'amount', 'desc').map((r) => r.id)).toEqual(['d', 'b', 'a', 'c'])
    expect(sortJournal(rows, 'amount', 'asc').map((r) => r.id)).toEqual(['c', 'a', 'b', 'd'])
  })

  it('не мутирует вход', () => {
    const before = rows.map((r) => r.id)
    sortJournal(rows, 'amount', 'asc')
    expect(rows.map((r) => r.id)).toEqual(before)
  })

  it('порядок устойчив при равных ключах', () => {
    const same = [
      tx({ id: 'x', date: '2026-07-01', amount: '10.00' }),
      tx({ id: 'y', date: '2026-07-01', amount: '10.00' }),
      tx({ id: 'z', date: '2026-07-01', amount: '10.00' }),
    ]
    expect(sortJournal(same, 'date', 'desc').map((r) => r.id)).toEqual(['x', 'y', 'z'])
  })
})
