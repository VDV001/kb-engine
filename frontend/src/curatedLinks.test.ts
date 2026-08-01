import { describe, expect, it } from 'vitest'
import type { Graph } from './api'
import { coverageNote, curatedRows, unplacedNote } from './curatedLinks'

const labels = { 'ai-agents-tools': 'AI-агенты', security: 'Безопасность', devops: 'DevOps' }

function graph(partial: Partial<Graph>): Graph {
  return { nodes: [], edges: [], labeled: 0, unlabeled: 0, label_count: 0, ...partial }
}

describe('curatedRows', () => {
  it('берёт только подписанные связи', () => {
    const g = graph({
      edges: [
        { from: 'ai-agents-tools', to: 'security', weight: 5, labels: ['prompt injection'] },
        { from: 'devops', to: 'security', weight: 3 },
      ],
      labeled: 1,
      unlabeled: 1,
      label_count: 1,
    })

    const rows = curatedRows(g, labels)

    expect(rows).toHaveLength(1)
    expect(rows[0].labels).toEqual(['prompt injection'])
    expect(rows[0].fromLabel).toBe('AI-агенты')
  })

  it('связь с несколькими смыслами идёт первой и не теряет ни одного', () => {
    const g = graph({
      edges: [
        { from: 'devops', to: 'security', weight: 3, labels: ['одна'] },
        { from: 'ai-agents-tools', to: 'security', weight: 5, labels: ['prompt injection', 'AI-SAFE', 'OAuth'] },
      ],
      labeled: 2,
      label_count: 4,
    })

    const rows = curatedRows(g, labels)

    expect(rows[0].labels).toHaveLength(3)
    expect(rows.flatMap((r) => r.labels)).toHaveLength(4)
  })
})

describe('coverageNote', () => {
  it('называет обе цифры, чтобы граф не читался как размеченный целиком', () => {
    const note = coverageNote(graph({ edges: new Array(245).fill({ from: 'a', to: 'b', weight: 1 }), labeled: 23, label_count: 32 }))

    expect(note).toContain('23')
    expect(note).toContain('245')
    expect(note).toContain('32')
  })

  it('говорит прямо, когда не подписано ничего', () => {
    const note = coverageNote(graph({ edges: [{ from: 'a', to: 'b', weight: 1 }], labeled: 0 }))

    expect(note).toContain('нет ни на одной')
  })
})

describe('unplacedNote', () => {
  it('молчит, когда все подписи размещены', () => {
    expect(unplacedNote(graph({ labeled: 3 }))).toBeNull()
  })

  it('сообщает о подписях, которым не нашлось связи', () => {
    const note = unplacedNote(graph({ unplaced_links: [{ from: 'x', to: 'y', label: 'негде' }] }))

    expect(note).toContain('1')
  })
})
