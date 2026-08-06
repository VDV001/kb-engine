import type { Transaction } from './api'
import { monthOf, toKopecks } from './money'

// Отбор и порядок строк журнала. Отдельно от компонента, потому что это
// единственная часть вида с настоящими краевыми случаями: включительные
// границы дат, складывающиеся критерии и сортировка сумм ЧИСЛАМИ, а не
// строками.

export interface JournalFilter {
  /** Набор YYYY-MM. Пустой или отсутствующий — «за всё время». */
  months?: string[]
  /** Точное имя категории. Пусто — любая. */
  category?: string
  /** Границы включительные, как их читает человек в поле «Дата от / до». */
  from?: string
  to?: string
  /** Подстрока по месту, описанию, категории и подкатегории. */
  search?: string
}

export type JournalSortField = 'date' | 'amount'
export type SortDirection = 'asc' | 'desc'

/** Строки, прошедшие все критерии. Порядок исходный, вход не меняется. */
export function filterJournal(rows: Transaction[], f: JournalFilter): Transaction[] {
  const months = f.months ?? []
  const query = (f.search ?? '').trim().toLowerCase()

  return rows.filter((t) => {
    if (months.length > 0 && !months.includes(monthOf(t.date))) return false
    if (f.category && (t.category ?? '') !== f.category) return false
    if (f.from && t.date < f.from) return false
    if (f.to && t.date > f.to) return false
    if (query) {
      const haystack = [t.place, t.description, t.category, t.subcategory]
      if (!haystack.some((v) => (v ?? '').toLowerCase().includes(query))) return false
    }
    return true
  })
}

/**
 * Ключ порядка внутри одного дня — момент ЗАПИСИ строки, посчитанный сервером
 * (поле recorded_at).
 *
 * Это не время траты — времени в книге нет вовсе, у строки есть только дата.
 * Но когда дата у двух строк одна, «что записали позже» — единственное
 * различие, которое вообще существует в данных.
 *
 * Пустое поле означает «момент неизвестен»: строку вписали в книгу мимо
 * движка. Такая строка считается самой ранней в своём дне — решение, названное
 * вслух, а не догадка о времени.
 *
 * Правило «что считать моментом» витрина НЕ переоткрывает: пока оно было
 * записано и здесь, и в движке, копии разошлись — здешняя отвергала строчный
 * ULID, который движок принимал, и принимала переполнение метки, которое
 * движок отвергал.
 *
 * Ключ обязан быть свойством ОДНОЙ строки, а не ответом про пару: сравнение,
 * отвечающее «равны» на пару с неизвестным моментом, теряет транзитивность, и
 * сортировка переставляет соседей, которые между собой сравнимы.
 */
function recordedKey(t: Transaction): string {
  return t.recorded_at ?? ''
}

/**
 * Новый массив в нужном порядке; вход не меняется.
 *
 * Суммы сравниваются в копейках. Строковое сравнение поставило бы «89.99»
 * выше «2500.50» — ровно та ошибка, ради которой деньги и держат целыми
 * числами.
 *
 * У сортировки по дате есть второй ключ — момент записи. Без него направление
 * действовало только на дни: внутри дня строки шли в порядке файла, и трата за
 * вчера, записанная сегодня, оказывалась в начале вчерашнего дня, а не в конце.
 */
export function sortJournal(
  rows: Transaction[],
  field: JournalSortField,
  dir: SortDirection,
): Transaction[] {
  const sign = dir === 'asc' ? 1 : -1
  // toSorted оставляет вход нетронутым, в отличие от sort.
  return rows.toSorted((a, b) => {
    if (field === 'amount') return sign * (toKopecks(a.amount) - toKopecks(b.amount))
    const byDate = a.date < b.date ? -1 : a.date > b.date ? 1 : 0
    if (byDate !== 0) return sign * byDate
    const x = recordedKey(a)
    const y = recordedKey(b)
    return sign * (x < y ? -1 : x > y ? 1 : 0)
  })
}
