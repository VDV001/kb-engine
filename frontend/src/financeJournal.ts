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

/** ULID: 26 символов Crockford base32 (без I, L, O, U). */
const ULID = /^[0-9A-HJKMNP-TV-Z]{26}$/

/**
 * Порядок по моменту ЗАПИСИ строки, вынутому из ULID (он начинается с метки
 * времени, поэтому сравнивается лексикографически).
 *
 * Это не время траты — времени в книге нет вовсе, у строки есть только дата.
 * Но когда дата у двух строк одна, «что записали позже» — единственное
 * различие, которое вообще существует в данных.
 *
 * Нечитаемый id (строку вписали в книгу мимо движка) даёт 0: момент неизвестен,
 * и выдуманный порядок хуже сохранённого. То же решение, что в расчёте баланса.
 */
function byRecordedAt(a: Transaction, b: Transaction): number {
  const x = a.id ?? ''
  const y = b.id ?? ''
  if (!ULID.test(x) || !ULID.test(y)) return 0
  return x < y ? -1 : x > y ? 1 : 0
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
    return sign * (byDate || byRecordedAt(a, b))
  })
}
