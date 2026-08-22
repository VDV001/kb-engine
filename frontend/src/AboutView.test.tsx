// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { AboutView, engineVersionLabel } from './AboutView'

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
    capabilities: async () => [
      { name: 'Каталог знаний', status: 'stable', note: 'записи и аудит' },
      { name: 'MCP-сервер над каталогом', status: 'roadmap', note: 'задача #273' },
    ],
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

  // Проверки ниже — про раскладку, но заданы структурой: jsdom высот не считает,
  // а разошедшиеся по высоте колонки и были дефектом. Оба утверждения держат
  // причину, а не следствие.
  //
  // История версий вдвое длиннее всего остального на странице. Пока она стояла
  // третьей карточкой в боковой колонке, левый столбец кончался на трети, и
  // пустое поле рядом дважды подряд прочиталось как поломка.
  it('история версий стоит под колонками, а не в боковой', async () => {
    changelog.value = {
      current_version: '0.23.1',
      current_date: '2026-07-27',
      current_tagline: '',
      releases: [{ version: '0.23.1', date: '2026-07-27', tagline: '', sections: { Добавлено: ['пункт'] } }],
    }
    const { container } = render(<AboutView stats={stats} onPickCategory={() => {}} />)
    const heading = await screen.findByText('Что нового в базе')
    const aside = container.querySelector('aside')
    expect(aside).not.toBeNull()
    expect(aside?.contains(heading)).toBe(false)
  })

  // Ящики идут двумя стопками: одной колонкой два с половиной десятка категорий
  // снова оказались бы вдвое выше соседа. Заодно проверяется арифметика деления
  // пополам — категория не должна ни потеряться, ни удвоиться.
  it('делит категории на две стопки, ничего не теряя', () => {
    const many = {
      ...stats,
      by_category: { a: 5, b: 4, c: 3, d: 2, e: 1 },
      category_labels: { a: 'Первая', b: 'Вторая', c: 'Третья', d: 'Четвёртая', e: 'Пятая' },
    }
    const { container } = render(<AboutView stats={many} onPickCategory={() => {}} />)
    const stacks = container.querySelectorAll('div.divide-y.border')
    expect(stacks.length).toBe(2)
    const names = Array.from(stacks).flatMap((s) =>
      Array.from(s.querySelectorAll('button')).map((b) => b.textContent),
    )
    expect(names.length).toBe(5)
    for (const label of ['Первая', 'Вторая', 'Третья', 'Четвёртая', 'Пятая']) {
      expect(names.filter((n) => n?.includes(label)).length).toBe(1)
    }
  })

  it('показывает статусную таблицу возможностей из /api/capabilities', async () => {
    render(<AboutView stats={stats} onPickCategory={() => {}} />)
    // Ждём асинхронную загрузку возможностей.
    expect(await screen.findByText('Возможности и их зрелость')).toBeTruthy()
    expect(screen.getByText('MCP-сервер над каталогом')).toBeTruthy()
    // Статус показан, а не проглочен: без него таблица не отличается от списка.
    expect(screen.getByText('🚧 roadmap')).toBeTruthy()
  })
})

// Go подставляет собственную псевдоверсию, когда бинарь собран не из тега:
// «v0.4.1-0.20260731180902-9f258b58e907+dirty». Для релиза это неважно, но
// движок открытый — собирать его из исходников будут постоянно, и на карточке
// такая строка ломается на две и читается как мусор. Дата и полный коммит в
// ней дублируют соседние строки «Сборка» и «Собран».
describe('engineVersionLabel', () => {
  const cases: [string, string][] = [
    ['v0.4.0', 'v0.4.0'],
    ['0.4.0', '0.4.0'],
    ['v0.4.1-0.20260731180902-9f258b58e907', 'v0.4.1-dev'],
    ['v0.4.1-0.20260731180902-9f258b58e907+dirty', 'v0.4.1-dev+правки'],
    ['v0.5.0+dirty', 'v0.5.0+правки'],
    ['dev', 'dev'],
    ['', '—'],
  ]
  it.each(cases)('%s → %s', (raw, want) => {
    expect(engineVersionLabel(raw)).toBe(want)
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
