// @vitest-environment jsdom
import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { HealthView } from './HealthView'
import type { Audits } from './api'

const linkHealth = vi.hoisted(() => ({
  value: {
    alive: 1004,
    moved: 179,
    gone: 5,
    undecidable: 122,
    unchecked: 0,
    with_url: 1310,
  } as unknown,
}))

vi.mock('./api', () => ({
  api: { linkHealth: async () => linkHealth.value },
}))

afterEach(cleanup)

const emptyAudits: Audits = { outdated: [], canonical: [], supersession: [] }

// Скан живости ссылок пишет результат в каталог с 01.08, но ни один экран его
// не показывал: сотня отказов 403 существовала только внутри файла. Витрина
// закрывает ровно это — работа, не доведённая до экрана, для читателя базы не
// существует.
describe('HealthView — здоровье ссылок', () => {
  it('показывает, что скан узнал про адреса', async () => {
    render(
      <HealthView audits={emptyAudits} duplicates={[]} entries={[]} onOpenEntry={() => {}} />,
    )
    expect(await screen.findByText(/1004/)).toBeDefined()
    expect(screen.getByText(/179/)).toBeDefined()
    expect(screen.getByText(/122/)).toBeDefined()
  })

  // 403 нельзя показывать как «мёртвая» или «живая»: habr отвечает им и на
  // снятую статью, и на бота. Подпись обязана называть это незнанием, иначе
  // экран соврёт увереннее, чем данные позволяют.
  it('называет неопределимые ссылки незнанием, а не поломкой', async () => {
    render(
      <HealthView audits={emptyAudits} duplicates={[]} entries={[]} onOpenEntry={() => {}} />,
    )
    expect(await screen.findByText(/не знаем|неизвестн/i)).toBeDefined()
  })

  // Правило 11 в его исходной формулировке: инструмент обязан вслух называть,
  // чего он НЕ проверил. Ноль непроверенных — тоже ответ, и его надо показать,
  // а не прятать как пустое место.
  it('называет число непроверенных даже когда оно ноль', async () => {
    linkHealth.value = { alive: 3, moved: 0, gone: 0, undecidable: 0, unchecked: 0, with_url: 3 }
    render(
      <HealthView audits={emptyAudits} duplicates={[]} entries={[]} onOpenEntry={() => {}} />,
    )
    expect(await screen.findByText(/не спрашивали|не провер/i)).toBeDefined()
  })
})
