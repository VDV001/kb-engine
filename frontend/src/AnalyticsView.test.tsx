// @vitest-environment jsdom
import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { AnalyticsView } from './AnalyticsView'
import type { AnalyticsConfig } from './api'

const engine = vi.hoisted(() => ({
  value: {
    version: '0.9.0',
    commit: 'abc1234',
    built: '2026-08-02T00:00:00Z',
    sources: [{ flag: 'analytics-config', connected: false }],
  } as unknown,
}))

vi.mock('./api', () => ({
  api: {
    graph: async () => ({ nodes: [], edges: [] }),
    engine: async () => engine.value,
  },
}))

afterEach(cleanup)

const stats = {
  total: 1395,
  by_category: { meta: 151 },
  by_lifecycle: {},
  by_verdict: {},
  by_kind: {},
  category_labels: { meta: 'Заметки' },
  health: { total: 1395, processed: 900, with_notes: 300, notes_base: 900 },
}

// Ровно то, что отдаёт сервер, когда семантический слой не подключён: все
// списки приходят как null (Go так пишет nil-слайс), а не отсутствуют.
const emptyConfig: AnalyticsConfig = {
  patterns: null,
  gaps: null,
  contradictions: null,
  manifesto_quotes: null,
}
const filledConfig: AnalyticsConfig = {
  ...emptyConfig,
  patterns: [{ name: 'Паттерн про память', clusters: ['meta'], desc: 'описание' }],
}

// Повод конкретный: дашборд подняли одним --catalog, четыре вкладки Аналитики
// оказались пусты, и экран читался как поломка релиза. На деле файл просто не
// попросили загрузить. Пустота обязана сама сказать, чего не хватает — это то
// же Правило 11, что уже применено к логу запуска.
describe('AnalyticsView — неподключённый семантический слой', () => {
  it('вместо пустоты называет флаг, которым подключается источник', async () => {
    render(<AnalyticsView config={emptyConfig} stats={stats} entries={[]} />)
    expect(await screen.findByText(/--analytics-config/)).toBeDefined()
  })

  it('когда источник подключён и в нём есть данные, объяснения нет', async () => {
    engine.value = {
      version: '0.9.0',
      commit: 'abc1234',
      built: '2026-08-02T00:00:00Z',
      sources: [{ flag: 'analytics-config', connected: true }],
    }
    render(<AnalyticsView config={filledConfig} stats={stats} entries={[]} />)
    expect(await screen.findByText(/Мета-аналитика/)).toBeDefined()
    expect(screen.queryByText(/--analytics-config/)).toBeNull()
  })

  it('подключённый, но пустой источник отличается от неподключённого', async () => {
    engine.value = {
      version: '0.9.0',
      commit: 'abc1234',
      built: '2026-08-02T00:00:00Z',
      sources: [{ flag: 'analytics-config', connected: true }],
    }
    render(<AnalyticsView config={emptyConfig} stats={stats} entries={[]} />)
    // Файл передали — значит виноват не запуск, а содержимое файла, и текст
    // не должен отправлять человека дописывать флаг, который у него уже есть.
    const note = await screen.findByText(/пуст/i)
    expect(note).toBeDefined()
    expect(screen.queryByText(/--analytics-config/)).toBeNull()
  })
})
