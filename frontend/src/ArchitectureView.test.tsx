// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { ArchitectureView } from './ArchitectureView'
import type { ArchMap, ArchMapIndexEntry } from './api'

const state = vi.hoisted(() => ({
  index: [] as ArchMapIndexEntry[],
  maps: {} as Record<string, unknown>,
}))

vi.mock('./api', () => ({
  api: {
    maps: async () => ({ maps: state.index }),
    map: async (id: string) => state.maps[id],
  },
}))

afterEach(cleanup)

const emptyStats = { nodes: 0, flows: 0, steps: 0, unverified: 0, findings: 0, runtime_checks: 0 }

function engineMap(): ArchMap {
  return {
    id: 'kb-engine',
    project: 'kb-engine',
    commit: 'abc1234',
    roots: [],
    layers: [{ id: 'commands', title: 'Команды', order: 1 }],
    zones: [{ name: 'Деньги', accepted: false, flows: 1, steps: 2 }],
    nodes: [
      { id: 'cmd-fin', title: 'fin', layer: 'commands', kind: 'service', sources: ['cmd/kbengine/fin.go:123'] },
      { id: 'ledger', title: 'Журнал', layer: 'commands', kind: 'data', sources: [] },
    ],
    flows: [
      {
        id: 'expense',
        title: 'Владелец записывает трату',
        summary: 'команда → журнал',
        zone: 'Деньги',
        steps: [
          { n: 1, from: 'cmd-fin', to: 'ledger', call: 'appendChecked', detail: 'единственный путь записи', source: 'cmd/kbengine/fin.go:210', unverified: false, branch: false },
          { n: 2, from: 'ledger', to: 'cmd-fin', call: 'financejsonl.Save', unverified: true, why: 'живьём не прогонялось', branch: false },
        ],
      },
    ],
    findings: [],
    gaps: [],
    examples: ['владелец записывает трату', 'страница сама говорит, что отстала'],
    runtime_checks: ['fin add на временном журнале: повтор отвергнут'],
    stats: { ...emptyStats, nodes: 2, flows: 1, steps: 2, unverified: 1, runtime_checks: 1 },
  }
}

function workspaceMap(): ArchMap {
  return {
    id: 'claude-cowork',
    project: 'claude-cowork',
    checked_at: '2026-08-08',
    note: 'числа о личной базе владельца убраны намеренно',
    roots: [{ name: 'cowork', path: '~/claude-cowork' }],
    layers: [],
    zones: [{ name: 'Автоматизация', accepted: true, note: 'принята: 7 сценариев', flows: 0, steps: 0 }],
    nodes: [],
    flows: [],
    findings: [
      { id: 'f-orphan', title: 'Сторож написан, но его никто не зовёт', severity: 'high', status: 'починено', evidence: 'cowork:digest/audit_watch.sh:38' },
    ],
    gaps: [],
    runtime_checks: [],
    acceptance: { classes_run: ['дубли заголовков — 0'], not_done: '', note: 'приёмка про смысл' },
    stats: { ...emptyStats, findings: 1 },
  }
}

function indexOf(...maps: ArchMap[]): ArchMapIndexEntry[] {
  return maps.map((m) => ({
    id: m.id,
    project: m.project,
    commit: m.commit,
    checked_at: m.checked_at,
    zones: m.zones.map((z) => z.name),
    stats: m.stats,
    accepted_zones: m.zones.filter((z) => z.accepted).length,
  }))
}

function setMaps(...maps: ArchMap[]) {
  state.index = indexOf(...maps)
  state.maps = Object.fromEntries(maps.map((m) => [m.id, m]))
}

describe('ArchitectureView', () => {
  // Пустая вкладка неотличима от сломанной, если не сказать, чего не хватает.
  // Ровно на этом четыре вкладки Аналитики полгода выглядели поломанными.
  it('без карт называет флаг, а не показывает пустоту', async () => {
    setMaps()
    render(<ArchitectureView />)
    expect(await screen.findByText(/--maps/)).toBeDefined()
  })

  it('открывает первую карту без лишнего клика', async () => {
    setMaps(engineMap(), workspaceMap())
    render(<ArchitectureView />)
    expect(await screen.findByText(/abc1234/)).toBeDefined()
  })

  it('переключает карту', async () => {
    setMaps(engineMap(), workspaceMap())
    render(<ArchitectureView />)
    fireEvent.click(await screen.findByRole('button', { name: 'claude-cowork' }))
    expect(await screen.findByText(/Коммита нет/)).toBeDefined()
    // У карты без коммита свежесть держится на пересчёте якорей, и говорить
    // об этом надо словами, а не отсутствием строки.
    expect(screen.getByText(/убраны намеренно/)).toBeDefined()
  })

  // Третий ответ. Зона без записи о приёмке — это «сверять не с чем», и
  // зелёная галочка здесь обещала бы проверку, которой не было.
  it('различает принятую зону и зону без записи о приёмке', async () => {
    setMaps(engineMap())
    render(<ArchitectureView />)
    fireEvent.click(await screen.findByRole('button', { name: 'Зоны' }))
    expect(await screen.findByText('сверять не с чем')).toBeDefined()

    cleanup()
    setMaps(workspaceMap())
    render(<ArchitectureView />)
    fireEvent.click(await screen.findByRole('button', { name: 'Зоны' }))
    expect(await screen.findByText('принята')).toBeDefined()
  })

  // «1 сценариев» — то, что видно на живой карте первым делом, и ровно та
  // ошибка, которую однажды уже ловили на «51 операций»: склонение в проекте
  // есть, но позвать его забыли.
  it('склоняет счётчики', async () => {
    const m = engineMap()
    m.zones = [{ name: 'Журнал версий', accepted: false, flows: 1, steps: 3 }]
    setMaps(m)
    render(<ArchitectureView />)
    fireEvent.click(await screen.findByRole('button', { name: 'Зоны' }))
    expect(await screen.findByText('1 сценарий · 3 шага')).toBeDefined()
  })

  // На живой карте not_done — не строка, а абзац: в узкой карточке шапки он
  // растягивает её вдвое против соседней. Место ему там, где читают приёмку.
  it('держит длинный текст приёмки в зонах, а не в шапке', async () => {
    const m = workspaceMap()
    m.acceptance = { classes_run: [], not_done: 'зона «Совет» живьём не прогонялась', note: '' }
    setMaps(m)
    render(<ArchitectureView />)
    // Шапка называет счёт принятых зон — короткую сводку, а не абзац.
    expect(await screen.findByText(/1 из 1/)).toBeDefined()
    fireEvent.click(screen.getByRole('button', { name: 'Зоны' }))
    expect(screen.getByText(/Совет.*не прогонялась/)).toBeDefined()

    // Уходим со вкладки зон: текст приёмки обязан уйти вместе с ними. Пока он
    // лежит в шапке, он виден на каждом разделе, включая те, где он не к месту.
    fireEvent.click(screen.getByRole('button', { name: 'Сценарии' }))
    expect(screen.queryByText(/Совет.*не прогонялась/)).toBeNull()
  })

  // Счётчики страницы-предшественницы наполовину про границу знания, а не про
  // объём: без «из них с якорем» и «шагов с источником» карта читается полной.
  it('показывает счётчики границы знания, а не только объём', async () => {
    setMaps(engineMap())
    render(<ArchitectureView />)
    expect(await screen.findByText('из них с якорем')).toBeDefined()
    expect(screen.getByText('шагов с источником')).toBeDefined()
    expect(screen.getByText('связь не подтверждена')).toBeDefined()
  })

  // Тип узла на схеме называется по-русски, а состояние отличается от сорта:
  // скрипт, которого никто не зовёт, выглядит как рабочий, и вся разница в
  // слове под значком.
  it('переводит типы узлов и называет сломанное состояние', async () => {
    const m = engineMap()
    m.nodes = [
      { id: 'cmd-fin', title: 'fin', layer: 'commands', kind: 'service', sources: ['x.go:1'] },
      { id: 'ledger', title: 'Сторож', layer: 'commands', kind: 'script-orphan', sources: [] },
    ]
    setMaps(m)
    render(<ArchitectureView />)
    expect((await screen.findAllByText('код')).length).toBeGreaterThan(0)
    expect(screen.getAllByText('никто не зовёт').length).toBeGreaterThan(0)
    expect(screen.getByText('узлов в сломанном состоянии')).toBeDefined()
  })

  it('объясняет, как читать карту, прямо над схемой', async () => {
    setMaps(engineMap())
    render(<ArchitectureView />)
    expect(await screen.findByText(/Выберите сценарий справа/)).toBeDefined()
    expect(screen.getByText(/нарисованная по памяти/)).toBeDefined()
  })

  // Правило 11 в чистом виде: число подсадок отрицательного контроля живёт в
  // сборке карты, а не в данных, и врать им нельзя.
  it('называет то, чего сама страница не знает', async () => {
    setMaps(engineMap())
    render(<ArchitectureView />)
    fireEvent.click(await screen.findByRole('button', { name: /Проверено/ }))
    expect(await screen.findByText(/Чего эта страница не знает/)).toBeDefined()
  })

  it('клик по зоне ведёт в её сценарии', async () => {
    setMaps(engineMap())
    render(<ArchitectureView />)
    fireEvent.click(await screen.findByRole('button', { name: 'Зоны' }))
    fireEvent.click(await screen.findByTitle('Показать сценарии этой зоны'))
    expect(await screen.findByText('Владелец записывает трату')).toBeDefined()
  })

  it('разворачивает шаги сценария и называет непроверенный вслух', async () => {
    setMaps(engineMap())
    render(<ArchitectureView />)
    fireEvent.click(await screen.findByRole('button', { name: 'Сценарии' }))
    fireEvent.click(await screen.findByText('Владелец записывает трату'))

    expect(await screen.findByText('appendChecked')).toBeDefined()
    expect(screen.getByText('не подтверждено')).toBeDefined()
    // Причина стоит рядом со своим шагом: собранная в отдельный список, она
    // перестаёт читаться как оговорка к конкретному утверждению.
    expect(screen.getByText(/живьём не прогонялось/)).toBeDefined()
  })

  // Узел без якорей — утверждение, ничем не подпёртое. Половина зоны
  // «Автоматизация» однажды состояла из таких, и увидели это не глазами.
  it('называет узел без единого якоря', async () => {
    setMaps(engineMap())
    render(<ArchitectureView />)
    fireEvent.click(await screen.findByRole('button', { name: 'Узлы' }))
    expect(await screen.findByText(/якорей нет/)).toBeDefined()
  })

  it('показывает находки вместе с тем, как их чинили', async () => {
    setMaps(workspaceMap())
    render(<ArchitectureView />)
    fireEvent.click(await screen.findByRole('button', { name: /Находки/ }))
    expect(await screen.findByText('Сторож написан, но его никто не зовёт')).toBeDefined()
    expect(screen.getByText('починено')).toBeDefined()
  })

  // Прогонов ноль и раздела прогонов нет — снаружи одинаково; вкладка обязана
  // различать. Правило 11: инструмент называет, чего он НЕ проверил.
  it('говорит, когда карта не подтверждена ни одним прогоном', async () => {
    setMaps(workspaceMap())
    render(<ArchitectureView />)
    fireEvent.click(await screen.findByRole('button', { name: /Проверено/ }))
    expect(await screen.findByText(/Ни одного прогона/)).toBeDefined()
  })

  // Фраза «не список модулей, а действия целиком» без примеров — утверждение ни
  // о чём. На странице-предшественнице примеры стояли рядом с ней и при
  // переносе пропали у обеих карт.
  it('показывает примеры действий из карты', async () => {
    setMaps(engineMap())
    render(<ArchitectureView />)
    expect(await screen.findByText(/владелец записывает трату/)).toBeDefined()
    expect(screen.getByText(/страница сама говорит, что отстала/)).toBeDefined()
  })

  // Карта без примеров показывает лид без них, а не выдуманные: пример,
  // сочинённый вкладкой, читался бы как факт о проекте.
  it('без примеров не выдумывает их', async () => {
    setMaps(workspaceMap())
    render(<ArchitectureView />)
    expect(await screen.findByText(/действия целиком/)).toBeDefined()
    // Примеры соседней карты — то, что появится, если вкладка зашьёт их в
    // вёрстку вместо чтения из данных. Проверка на «нет никаких примеров»
    // такую подсадку пропускала: она проходила и до починки, и после неё.
    expect(screen.queryByText(/владелец записывает трату/)).toBeNull()
  })
})

// Раздел «Разбор» — страница, написанную человеком, прямо в карте. Появляется
// он только у карты, которая эту страницу называет: кнопка, открывающая
// пустоту, читается как поломка, а её отсутствие — как «здесь этого нет».
describe('раздел «Разбор»', () => {
  it('не показывается у карты без page', async () => {
    state.index = [{ id: 'kb-engine', project: 'kb-engine', zones: [], stats: emptyStats, accepted_zones: 0 }]
    state.maps = { 'kb-engine': engineMap() }
    render(<ArchitectureView />)
    expect(await screen.findByText('Схема')).toBeTruthy()
    expect(screen.queryByText('Разбор')).toBeNull()
  })

  it('показывается у карты с page и грузит её маршрутом /kb/', async () => {
    const m = engineMap()
    m.page = 'creations/projects/2026-08-15_x/x.html'
    state.index = [{ id: 'kb-engine', project: 'kb-engine', zones: [], stats: emptyStats, accepted_zones: 0 }]
    state.maps = { 'kb-engine': m }
    render(<ArchitectureView />)

    const chip = await screen.findByText('Разбор')
    fireEvent.click(chip)

    const frame = document.querySelector('iframe')
    expect(frame).toBeTruthy()
    // Именно /kb/, а не file:// и не путь на диске: маршрут отдаёт только то,
    // что каталог называет, и это единственный способ открыть артефакт базы.
    expect(frame?.getAttribute('src')).toBe('/kb/creations/projects/2026-08-15_x/x.html')
  })
})
