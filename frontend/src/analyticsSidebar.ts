import type { Entry } from './api'
import { dateOf } from './catalog'

// Оба расчёта берут dateOf — то есть date_added, а при его отсутствии
// date_created. Исходный дашборд считал ТОЛЬКО по date_created, и на этой базе
// это другой факт: 862 записи несут только date_added, 461 только
// date_created. По исходной формуле «рост за 12 недель» получался +92, потому
// что считалась треть базы; здесь выходит +582, потому что считается вся.
// Вопрос «когда категорию пополняли» отвечает дата попадания в базу, а не
// дата, когда автор написал статью. Лента активности на дашборде считает так
// же — два вида, отвечающие на один вопрос, обязаны отвечать одинаково.

/** Категория, которую давно не пополняли. */
export interface StaleCategory {
  category: string
  /** Полных недель с последнего пополнения. */
  weeks: number
  /** Размер категории целиком: вопрос «сколько знания стынет», а не
   * «сколько записей просрочено». */
  count: number
}

const WEEK_MS = 7 * 24 * 60 * 60 * 1000

/**
 * staleCategories ranks categories by how long it has been since anything was
 * added to them. A category with no dated entry at all is left out rather than
 * ranked as infinitely stale: absence of a date is absence of an answer.
 */
export function staleCategories(entries: Entry[], today: Date, limit: number): StaleCategory[] {
  const latest = new Map<string, number>()
  const size = new Map<string, number>()
  for (const e of entries) {
    size.set(e.category, (size.get(e.category) ?? 0) + 1)
    const d = dateOf(e)
    if (!d) continue
    const t = Date.parse(d)
    if (Number.isNaN(t)) continue
    latest.set(e.category, Math.max(latest.get(e.category) ?? 0, t))
  }
  return [...latest.entries()]
    .map(([category, t]) => ({
      category,
      weeks: Math.floor((today.getTime() - t) / WEEK_MS),
      count: size.get(category) ?? 0,
    }))
    .sort((a, b) => b.weeks - a.weeks)
    .slice(0, limit)
}

/** Рост по неделям: сколько записей добавлено в каждую из последних недель. */
export interface Growth {
  weeks: number[]
  total: number
  /** Среднее за неделю, округлённое до десятой: «7.7/нед» как в исходнике. */
  perWeek: number
}

/**
 * weeklyGrowth buckets entries into the last n weeks, oldest first. The window
 * ends today, so the rightmost bucket is the current, partial week — that is
 * what «за последние n недель» means to a reader.
 */
export function weeklyGrowth(entries: Entry[], today: Date, n: number): Growth {
  const weeks = new Array<number>(n).fill(0)
  const end = today.getTime()
  for (const e of entries) {
    const d = dateOf(e)
    if (!d) continue
    const t = Date.parse(d)
    if (Number.isNaN(t)) continue
    const back = Math.floor((end - t) / WEEK_MS)
    if (back < 0 || back >= n) continue
    weeks[n - 1 - back]++
  }
  const total = weeks.reduce((s, x) => s + x, 0)
  return { weeks, total, perWeek: Math.round((total / n) * 10) / 10 }
}
