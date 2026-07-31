import { describe, expect, it } from 'vitest'
import type { Graph } from './api'
import { layoutGraph } from './graphLayout'

// Категории по убыванию — так их и отдаёт сервер.
const graph: Graph = {
  nodes: [
    { category: 'golang', count: 100 },
    { category: 'meta', count: 60 },
    { category: 'local-ai', count: 30 },
    { category: 'career', count: 20 },
    { category: 'ux', count: 10 },
    { category: 'devops', count: 5 },
    { category: 'очень-длинное-имя-категории', count: 3 },
  ],
  edges: [
    { from: 'golang', to: 'meta', weight: 9 },
    { from: 'golang', to: 'ux', weight: 1 },
    { from: 'meta', to: 'нет-такой', weight: 4 },
  ],
}

const box = { width: 900, height: 580 }

describe('layoutGraph', () => {
  it('puts the biggest category at the centre of the canvas', () => {
    const l = layoutGraph(graph, box)
    const hub = l.nodes.find((n) => n.isHub)
    expect(hub?.key).toBe('golang')
    expect(hub?.x).toBeCloseTo(l.canvasWidth / 2, 6)
    expect(hub?.y).toBeCloseTo(box.height / 2, 6)
    expect(l.nodes.filter((n) => n.isHub)).toHaveLength(1)
  })

  // Размер узла — тоже данные: по нему видно вес категории, не читая цифру.
  it('sizes nodes by their share of the biggest category', () => {
    const l = layoutGraph(graph, box)
    const size = (key: string) => {
      const n = l.nodes.find((x) => x.key === key)!
      return n.width * n.height
    }
    expect(size('golang')).toBeGreaterThan(size('meta'))
    expect(size('meta')).toBeGreaterThan(size('ux'))
    expect(size('ux')).toBeGreaterThanOrEqual(size('devops'))
  })

  // Узел, вылезший за край, обрезается контейнером — а обрезанная подпись
  // выглядит поломкой вёрстки, хотя данные целы.
  it('keeps every node fully inside the canvas', () => {
    for (const b of [box, { width: 420, height: 360 }, { width: 1600, height: 900 }]) {
      const l = layoutGraph(graph, b)
      for (const n of l.nodes) {
        expect(n.x - n.width / 2).toBeGreaterThanOrEqual(0)
        expect(n.x + n.width / 2).toBeLessThanOrEqual(l.canvasWidth)
        expect(n.y - n.height / 2).toBeGreaterThanOrEqual(0)
        expect(n.y + n.height / 2).toBeLessThanOrEqual(b.height)
      }
    }
  })

  // Ребро, чей конец не попал в раскладку, рисовать не из чего: сервер отдаёт
  // связи по всему каталогу, а на холст берётся только верхушка.
  it('drops edges whose endpoint is not on the canvas', () => {
    const l = layoutGraph(graph, box)
    expect(l.edges).toHaveLength(2)
    expect(l.edges.every((e) => Number.isFinite(e.x1) && Number.isFinite(e.y1))).toBe(true)
  })

  // Сильные связи — сплошные, слабые — пунктир: иначе плотный граф читается
  // как равномерная сетка, где всё связано со всем одинаково.
  it('marks strong links apart from weak ones', () => {
    const l = layoutGraph(graph, box)
    const strong = l.edges.find((e) => e.from === 'golang' && e.to === 'meta')
    const weak = l.edges.find((e) => e.from === 'golang' && e.to === 'ux')
    expect(strong?.strong).toBe(true)
    expect(weak?.strong).toBe(false)
    expect(strong!.strokeWidth).toBeGreaterThan(weak!.strokeWidth)
    expect(strong!.opacity).toBeGreaterThan(weak!.opacity)
  })

  // Номер нужен боковой панели: список слева и узлы на холсте — одно и то же,
  // и нумерация должна совпадать, а не строиться каждым местом заново.
  it('numbers nodes from one, in the order given', () => {
    const l = layoutGraph(graph, box)
    expect(l.nodes.map((n) => n.index)).toEqual([1, 2, 3, 4, 5, 6, 7])
    expect(l.nodes[0].key).toBe('golang')
  })

  // Та же база — та же картинка: иначе вчерашний снимок не с чем сравнивать.
  it('is deterministic', () => {
    expect(layoutGraph(graph, box)).toEqual(layoutGraph(graph, box))
  })

  it('survives an empty catalog', () => {
    const l = layoutGraph({ nodes: [], edges: [] }, box)
    expect(l.nodes).toHaveLength(0)
    expect(l.edges).toHaveLength(0)
  })
})

// Кольцо теперь означает связь с ядром, а не размер: размер уже показан числом
// на узле и площадью прямоугольника, и третий канал под то же самое —
// потраченная ось. Ближнее кольцо читается как «грани основной линии»,
// дальнее — как «острова со своим словарём».
describe('layoutGraph: слои по связи с ядром', () => {
  const byLink: Graph = {
    nodes: [
      { category: 'hub', count: 100 },
      { category: 'big-island', count: 90 }, // крупная, но с ядром не связана
      { category: 'small-core', count: 4 }, // мелкая, но связана сильно
      { category: 'mid', count: 40 },
      { category: 'lonely', count: 30 },
    ],
    edges: [
      { from: 'hub', to: 'small-core', weight: 12 },
      { from: 'hub', to: 'mid', weight: 5 },
      { from: 'big-island', to: 'mid', weight: 7 }, // связь мимо ядра не приближает
    ],
  }

  it('ставит сильно связанную с ядром категорию ближе, даже если она мелкая', () => {
    const l = layoutGraph(byLink, box)
    const ring = (key: string) => l.nodes.find((n) => n.key === key)!.ring

    expect(ring('hub')).toBe('core')
    expect(ring('small-core')).toBe('inner')
    expect(ring('big-island')).toBe('outer')
  })

  it('кладёт категорию без связей с ядром во внешнее кольцо', () => {
    const l = layoutGraph(byLink, box)
    const lonely = l.nodes.find((n) => n.key === 'lonely')!
    expect(lonely.linkToHub).toBe(0)
    expect(lonely.ring).toBe('outer')
  })

  it('считает связь с ядром весом ребра, в любую сторону', () => {
    const l = layoutGraph(byLink, box)
    expect(l.nodes.find((n) => n.key === 'small-core')!.linkToHub).toBe(12)
    expect(l.nodes.find((n) => n.key === 'mid')!.linkToHub).toBe(5)
    // Связь big-island↔mid идёт мимо ядра и на близость не влияет.
    expect(l.nodes.find((n) => n.key === 'big-island')!.linkToHub).toBe(0)
  })

  it('чем сильнее связь, тем ближе узел к центру', () => {
    const l = layoutGraph(byLink, box)
    const dist = (key: string) => {
      const n = l.nodes.find((x) => x.key === key)!
      return Math.hypot(n.x - l.canvasWidth / 2, n.y - box.height / 2)
    }
    expect(dist('small-core')).toBeLessThan(dist('lonely'))
    expect(dist('mid')).toBeLessThan(dist('big-island'))
  })

  // Прямоугольники не должны наезжать друг на друга: прежняя раскладка держала
  // это тем, что мелкие уходили на длинную внешнюю дугу, а порядок теперь другой.
  it('не даёт прямоугольникам перекрываться', () => {
    const l = layoutGraph(graph, box)
    for (let i = 0; i < l.nodes.length; i++) {
      for (let j = i + 1; j < l.nodes.length; j++) {
        const a = l.nodes[i]
        const b = l.nodes[j]
        const apart =
          Math.abs(a.x - b.x) >= (a.width + b.width) / 2 ||
          Math.abs(a.y - b.y) >= (a.height + b.height) / 2
        expect(apart, `${a.key} перекрывает ${b.key}`).toBe(true)
      }
    }
  })
})
