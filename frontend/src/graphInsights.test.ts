import { describe, expect, it } from 'vitest'
import type { Graph } from './api'
import { graphInsights } from './graphInsights'
import { layoutGraph } from './graphLayout'

const box = { width: 900, height: 580 }

// Числа взяты близкими к живой базе: там общих тегов с ядром от 4 до 95, и
// абсолютная величина почти повторяет размер категории. Различает только
// плотность — сколько общих тегов приходится на одну запись.
const graph: Graph = {
  nodes: [
    { category: 'ai-agents', count: 300 },
    { category: 'meta', count: 150 }, // много тегов, но на запись мало — остров
    { category: 'claude', count: 117 },
    { category: 'databases', count: 21 }, // самый разреженный
    { category: 'knowledge', count: 13 }, // плотный
    { category: 'tiny', count: 2 }, // ниже порога заметности
  ],
  edges: [
    { from: 'ai-agents', to: 'meta', weight: 79 }, // 0.53 на запись
    { from: 'ai-agents', to: 'claude', weight: 95 }, // 0.81
    { from: 'ai-agents', to: 'databases', weight: 6 }, // 0.29
    { from: 'ai-agents', to: 'knowledge', weight: 22 }, // 1.69
    { from: 'ai-agents', to: 'tiny', weight: 8 }, // 4.0, но категория крошечная
  ],
}

const insights = graphInsights(layoutGraph(graph, box), { total: 1000 })

describe('graphInsights', () => {
  it('называет ядро с его долей от базы', () => {
    expect(insights.core?.key).toBe('ai-agents')
    expect(insights.core?.count).toBe(300)
    expect(insights.core?.share).toBe(30)
  })

  // «Связана с 23 категориями из 23» — не вывод, а константа: на живой базе
  // ядро делит теги вообще со всеми. Считать надо тесно связанные.
  it('считает ядром связанными только те, у кого хотя бы тег на запись', () => {
    expect(insights.core?.closeCount).toBe(1) // knowledge 1.69; claude 0.81 уже нет
  })

  it('в сросшиеся берёт самые плотные по тегам на запись, а не самые крупные', () => {
    const keys = insights.fused.map((f) => f.key)
    expect(keys[0]).toBe('knowledge')
    expect(keys).not.toContain('meta') // крупнейшая по абсолютной связи, но разрежена
  })

  it('в острова берёт самые разреженные', () => {
    const keys = insights.islands.map((i) => i.key)
    expect(keys[0]).toBe('databases')
    expect(keys).toContain('meta')
    expect(keys).not.toContain('knowledge')
  })

  // Крошечная категория даёт шумное отношение: восемь общих тегов на две
  // записи — это про то, что у записи много меток, а не про близость темы.
  it('не пускает в выводы категории мельче процента базы', () => {
    expect(insights.fused.map((f) => f.key)).not.toContain('tiny')
    expect(insights.islands.map((i) => i.key)).not.toContain('tiny')
  })

  it('показывает плотность, по которой отобрал', () => {
    const k = insights.fused.find((f) => f.key === 'knowledge')
    expect(k?.perEntry).toBeCloseTo(1.69, 1)
    expect(k?.weight).toBe(22)
  })

  it('остров помнит, с чем он всё-таки связан крепче всего', () => {
    const db = insights.islands.find((i) => i.key === 'databases')
    expect(db?.linkedTo).toBe('ai-agents')
  })

  it('не выдумывает выводов на пустом графе', () => {
    const empty = graphInsights(layoutGraph({ nodes: [], edges: [] }, box), { total: 0 })
    expect(empty.core).toBeNull()
    expect(empty.fused).toHaveLength(0)
    expect(empty.islands).toHaveLength(0)
  })

  it('не ставит категорию разом и в сросшиеся, и в острова', () => {
    const fused = new Set(insights.fused.map((f) => f.key))
    expect(insights.islands.every((i) => !fused.has(i.key))).toBe(true)
  })

  it('не считает ядро связанным само с собой', () => {
    expect(insights.fused.map((f) => f.key)).not.toContain('ai-agents')
    expect(insights.islands.map((i) => i.key)).not.toContain('ai-agents')
  })
})
