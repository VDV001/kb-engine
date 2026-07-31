import { describe, expect, it } from 'vitest'
import type { Graph } from './api'
import { graphInsights } from './graphInsights'
import { layoutGraph } from './graphLayout'

const box = { width: 900, height: 580 }

const graph: Graph = {
  nodes: [
    { category: 'ai-agents', count: 300 },
    { category: 'meta', count: 120 },
    { category: 'devops', count: 90 },
    { category: 'typescript', count: 80 },
    { category: 'career', count: 60 },
    { category: 'ux', count: 4 },
  ],
  edges: [
    { from: 'ai-agents', to: 'meta', weight: 11 },
    { from: 'ai-agents', to: 'devops', weight: 4 },
    { from: 'ai-agents', to: 'career', weight: 1 },
    { from: 'devops', to: 'typescript', weight: 6 },
  ],
}

describe('graphInsights', () => {
  const insights = graphInsights(layoutGraph(graph, box), { total: 1000 })

  it('называет ядро с его долей от базы', () => {
    expect(insights.core?.key).toBe('ai-agents')
    expect(insights.core?.count).toBe(300)
    expect(insights.core?.share).toBe(30)
  })

  it('в сросшиеся берёт только сильные связи с ядром', () => {
    const keys = insights.fused.map((f) => f.key)
    expect(keys).toContain('meta')
    expect(keys).toContain('devops')
    // Один общий тег — совпадение, а не общая область.
    expect(keys).not.toContain('career')
  })

  // Остров — тема со своим словарём. Мелочь островом называть незачем: у неё
  // просто мало тегов, и вывод «изолирована» ничего не значит.
  it('в острова берёт заметные категории без связи с ядром', () => {
    const keys = insights.islands.map((i) => i.key)
    expect(keys).toContain('typescript')
    expect(keys).not.toContain('ux')
    expect(keys).not.toContain('meta')
  })

  it('остров помнит, с чем он всё-таки связан', () => {
    const ts = insights.islands.find((i) => i.key === 'typescript')
    expect(ts?.linkedTo).toBe('devops')
  })

  it('не выдумывает выводов на пустом графе', () => {
    const empty = graphInsights(layoutGraph({ nodes: [], edges: [] }, box), { total: 0 })
    expect(empty.core).toBeNull()
    expect(empty.fused).toHaveLength(0)
    expect(empty.islands).toHaveLength(0)
  })

  it('не считает ядро связанным само с собой', () => {
    expect(insights.fused.map((f) => f.key)).not.toContain('ai-agents')
    expect(insights.islands.map((i) => i.key)).not.toContain('ai-agents')
  })
})
