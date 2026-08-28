import { describe, expect, it } from 'vitest'
import { groupByDay, summarise } from './answers'
import type { ToolCall } from './api'

const call = (tool: string, at: string, query = '', ok = true): ToolCall => ({ tool, at, query, ok })

describe('журнал ответов', () => {
  // Число вызовов — не число вопросов: агент спрашивает базу несколько раз на
  // один вопрос человека, и сводка обязана называть обе величины, иначе «43
  // вызова» читается как «43 раза спрашивал я».
  it('считает вызовы, отказы и поиски отдельно', () => {
    const s = summarise([
      call('search_catalog', '2026-08-28T14:00:00Z', 'ddd'),
      call('search_catalog', '2026-08-28T13:00:00Z', 'harness'),
      call('get_entry', '2026-08-28T12:00:00Z', '#9999', false),
      call('stats', '2026-08-28T11:00:00Z'),
    ])
    expect(s).toEqual({ total: 4, failed: 1, searches: 2, days: 1 })
  })

  it('пустой журнал не выдумывает нулевого дня', () => {
    expect(summarise([])).toEqual({ total: 0, failed: 0, searches: 0, days: 0 })
  })

  // Группировка по дню, а не сплошной лентой: вопрос «что спрашивали вчера»
  // задают чаще, чем «что было двести вызовов назад».
  it('группирует по дню, новейший день первым', () => {
    const days = groupByDay([
      call('stats', '2026-08-27T10:00:00Z'),
      call('search_catalog', '2026-08-28T09:00:00Z', 'go'),
      call('get_entry', '2026-08-28T08:00:00Z', '#1'),
    ])
    expect(days.map((d) => d.day)).toEqual(['2026-08-28', '2026-08-27'])
    expect(days[0].calls).toHaveLength(2)
    // Внутри дня — тоже новейшее сверху: порядок не должен переворачиваться
    // на границе группировки.
    expect(days[0].calls[0].query).toBe('go')
  })
})
