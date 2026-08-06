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
    // Фикстура локальная и заведомо НЕ отсортированная. На общей фикстуре модуля
    // этот тест был слепым: к моменту прогона она уже стояла в нужном порядке, и
    // сортировка на месте прошла бы незамеченной.
    const local = [
      tx({ id: 'm1', date: '2026-07-15', amount: '30.00' }),
      tx({ id: 'm2', date: '2026-06-20', amount: '10.00' }),
      tx({ id: 'm3', date: '2026-08-02', amount: '20.00' }),
    ]
    const before = local.map((r) => r.id)
    sortJournal(local, 'amount', 'asc')
    sortJournal(local, 'date', 'desc')
    expect(local.map((r) => r.id)).toEqual(before)
  })

  it('внутри одного дня решает момент записи, а не порядок строк в файле', () => {
    // У траты есть дата, но нет времени — в книге времени нет вовсе. Момент
    // записи считает движок и отдаёт полем recorded_at; витрина его только
    // сравнивает.
    //
    // Случай, ради которого правило заведено: трата за вчера, записанная сегодня.
    // По дате она вчерашняя, но записана позже всех вчерашних — и при сортировке
    // «сначала новое» должна стоять первой в своём дне, а не падать в его конец
    // вслед за порядком строк в файле.
    const sameDay = [
      tx({ id: 'a', date: '2026-08-05', amount: '111.00', recorded_at: '2026-08-05T09:00:00Z' }),
      tx({ id: 'b', date: '2026-08-05', amount: '222.00', recorded_at: '2026-08-05T12:00:00Z' }),
      tx({ id: 'c', date: '2026-08-06', amount: '333.00', recorded_at: '2026-08-06T08:00:00Z' }),
    ]
    expect(sortJournal(sameDay, 'date', 'desc').map((r) => r.amount)).toEqual(['333.00', '222.00', '111.00'])
    expect(sortJournal(sameDay, 'date', 'asc').map((r) => r.amount)).toEqual(['111.00', '222.00', '333.00'])
    // и наоборот: трата за вчера, записанная сегодня, встаёт первой во ВЧЕРАШНЕМ дне
    const backdated = [...sameDay, tx({ id: 'd', date: '2026-08-05', amount: '444.00', recorded_at: '2026-08-06T10:00:00Z' })]
    expect(sortJournal(backdated, 'date', 'desc').map((r) => r.amount)).toEqual(['333.00', '444.00', '222.00', '111.00'])
  })

  it('строка без момента записи не ломает порядок остальных', () => {
    // Строку могли вписать в книгу мимо движка — тогда ULID нет, и момент записи
    // неизвестен. Такая строка считается самой ранней в своём дне: это решение,
    // названное вслух, а не догадка о том, когда её на самом деле записали.
    //
    // Проверяется главное — что ОСТАЛЬНЫЕ строки дня не теряют порядок. Прежняя
    // версия отвечала «равны» на любую пару с нечитаемым id, и компаратор
    // переставал быть транзитивным: сортировка выдавала произвольную
    // перестановку. На трёх строках это было незаметно, на четырёх — уже нет.
    const day = ['01', '02', '03', '04', '05'].map((n, i) =>
      tx({ id: `u${n}`, date: '2026-08-05', amount: `${i + 1}.00`, recorded_at: `2026-08-05T${n}:00:00Z` }),
    )
    const mixed = [...day.slice(0, 2), tx({ id: 'вписана в книгу руками', date: '2026-08-05', amount: '99.00' }), ...day.slice(2)]

    const desc = sortJournal(mixed, 'date', 'desc').map((r) => r.amount)
    expect(desc.filter((a) => a !== '99.00')).toEqual(['5.00', '4.00', '3.00', '2.00', '1.00'])
    expect(desc.at(-1)).toBe('99.00')

    const asc = sortJournal(mixed, 'date', 'asc').map((r) => r.amount)
    expect(asc.filter((a) => a !== '99.00')).toEqual(['1.00', '2.00', '3.00', '4.00', '5.00'])
    expect(asc[0]).toBe('99.00')
  })

  it('несколько строк без момента сохраняют между собой порядок файла', () => {
    const rows = [
      tx({ id: 'expense-r12', date: '2026-08-05', amount: '10.00' }),
      tx({ id: 'expense-r13', date: '2026-08-05', amount: '20.00' }),
      tx({ id: 'u1', date: '2026-08-05', amount: '30.00', recorded_at: '2026-08-05T10:00:00Z' }),
    ]
    expect(sortJournal(rows, 'date', 'desc').map((r) => r.amount)).toEqual(['30.00', '10.00', '20.00'])
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
