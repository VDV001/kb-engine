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
    runtime_checks: ['fin add на временном журнале: повтор отвергнут'],
    stats: { ...emptyStats, nodes: 2, flows: 1, steps: 2, unverified: 1, runtime_checks: 1 },
  }
}

function workspaceMap(): ArchMap {
  return {
    id: 'claude-cowork',
    project: 'claude-cowork',
    checked_at: '2026-08-08',
    note: 'проект не под git',
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
    expect(await screen.findByText(/Деньги/)).toBeDefined()
    expect(screen.getByText(/abc1234/)).toBeDefined()
  })

  it('переключает карту', async () => {
    setMaps(engineMap(), workspaceMap())
    render(<ArchitectureView />)
    fireEvent.click(await screen.findByRole('button', { name: 'claude-cowork' }))
    expect(await screen.findByText(/Автоматизация/)).toBeDefined()
    // У карты без коммита свежесть держится на пересчёте якорей, и говорить
    // об этом надо словами, а не отсутствием строки.
    expect(screen.getByText(/Коммита нет/)).toBeDefined()
  })

  // Третий ответ. Зона без записи о приёмке — это «сверять не с чем», и
  // зелёная галочка здесь обещала бы проверку, которой не было.
  it('различает принятую зону и зону без записи о приёмке', async () => {
    setMaps(engineMap())
    render(<ArchitectureView />)
    expect(await screen.findByText('сверять не с чем')).toBeDefined()

    cleanup()
    setMaps(workspaceMap())
    render(<ArchitectureView />)
    expect(await screen.findByText('принята')).toBeDefined()
  })

  it('клик по зоне ведёт в её сценарии', async () => {
    setMaps(engineMap())
    render(<ArchitectureView />)
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
})
