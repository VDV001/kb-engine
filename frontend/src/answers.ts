// Что агент спрашивал у базы — чистые правила вкладки «Ответы».
//
// Отдельным модулем от вида по той же причине, что и полоса контекста: сводка
// и группировка — правила, а не разметка, и проверяться должны без монтирования
// дерева. Вид остаётся тонким.

import type { ToolCall } from './api'

export type Summary = {
  total: number
  failed: number
  searches: number
  /** Сколько разных дней в журнале — горизонт, без которого total ни о чём. */
  days: number
}

/**
 * Сводка журнала.
 *
 * ⚠️ Число вызовов — НЕ число вопросов человека: на один вопрос агент нередко
 * зовёт базу несколько раз. Поэтому названы обе величины отдельно, а «поиски»
 * не выдаются за «спросил владелец».
 */
export function summarise(calls: ToolCall[]): Summary {
  const days = new Set<string>()
  let failed = 0
  let searches = 0
  for (const c of calls) {
    days.add(dayOf(c.at))
    if (!c.ok) failed++
    if (c.tool === 'search_catalog') searches++
  }
  return { total: calls.length, failed, searches, days: days.size }
}

export type Day = { day: string; calls: ToolCall[] }

/** Группировка по дню, новейший день первым и новейший вызов первым внутри дня. */
export function groupByDay(calls: ToolCall[]): Day[] {
  const byDay = new Map<string, ToolCall[]>()
  // Сортируется копия: порядок, пришедший с сервера, — его дело, и
  // переворачивать чужой массив на месте значит менять то, что тебе одолжили.
  const sorted = [...calls].sort((a, b) => b.at.localeCompare(a.at))
  for (const c of sorted) {
    const day = dayOf(c.at)
    const list = byDay.get(day)
    if (list) list.push(c)
    else byDay.set(day, [c])
  }
  return [...byDay.entries()].map(([day, list]) => ({ day, calls: list }))
}

// День берётся из строки, а не из Date: сервер отдаёт момент со смещением, и
// пересчёт в местное время подвинул бы границу дня у вечерних вызовов.
function dayOf(at: string): string {
  return at.slice(0, 10)
}
