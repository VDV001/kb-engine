// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { AboutView } from './AboutView'

const changelog = vi.hoisted(() => ({
  value: {
    current_version: '0.23.1',
    current_date: '2026-07-27',
    current_tagline: 'хвосты после канонизации',
    releases: [
      { version: '0.23.1', date: '2026-07-27', tagline: '', sections: { Добавлено: ['пункт'] } },
    ],
  } as unknown,
}))

vi.mock('./api', () => ({
  api: {
    changelog: async () => changelog.value,
    engine: async () => ({ version: '0.4.1', commit: '665868a195ab', built: '2026-07-31T17:21:48Z' }),
  },
}))

afterEach(cleanup)

const stats = {
  total: 1340,
  by_category: { 'ai-agents-tools': 302, meta: 151 },
  by_lifecycle: {},
  by_verdict: {},
  by_kind: {},
  category_labels: {
    'ai-agents-tools': 'AI-агенты и MCP: инструменты и протоколы',
    meta: 'Заметки, планы, идеи',
  },
  health: { total: 1340, processed: 900, with_notes: 300, notes_base: 900 },
}

describe('AboutView', () => {
  // Версия движка живёт только в бинаре: до этого её нельзя было увидеть,
  // не выйдя в терминал за `kbengine version`.
  it('показывает версию движка и коммит', async () => {
    render(<AboutView stats={stats} onPickCategory={() => {}} />)
    expect(await screen.findByText(/0\.4\.1/)).toBeDefined()
    expect(screen.getByText(/665868a/)).toBeDefined()
  })

  // Ключи категорий («ai-agents-tools») — это имена для машины. Читаемые
  // названия каталог уже несёт, и Архив их показывает; здесь они терялись.
  it('показывает читаемые названия категорий, а не ключи', () => {
    render(<AboutView stats={stats} onPickCategory={() => {}} />)
    expect(screen.getByText('AI-агенты и MCP')).toBeDefined()
    expect(screen.queryByText('ai-agents-tools')).toBeNull()
  })

  it('клик по категории отдаёт наверх её ключ, а не название', () => {
    const picked: string[] = []
    render(<AboutView stats={stats} onPickCategory={(c) => picked.push(c)} />)
    fireEvent.click(screen.getByText('AI-агенты и MCP'))
    expect(picked).toEqual(['ai-agents-tools'])
  })
})

describe('AboutView, когда changelog не разобран', () => {
  // Живой случай: вместо CHANGELOG.md движку подсунули changelog.json, парсер
  // markdown не нашёл ни одного заголовка и отдал пустой документ. Страница
  // печатала «v0.0.0 · —», и ноль выглядел фактом о базе.
  it('говорит, что файл не разобран, вместо версии 0.0.0', async () => {
    changelog.value = { current_version: '0.0.0', current_date: null, current_tagline: '', releases: [] }
    render(<AboutView stats={stats} onPickCategory={() => {}} />)
    expect(await screen.findByText(/не разобран/i)).toBeDefined()
    expect(screen.queryByText(/v0\.0\.0/)).toBeNull()
  })
})
