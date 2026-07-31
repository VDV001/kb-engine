// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { SettingsView } from './views'

vi.mock('./api', () => ({
  api: {
    changelog: async () => ({
      current_version: '0.3.0',
      current_date: '2026-07-31',
      current_tagline: 'перенос дашборда',
      releases: Array.from({ length: 7 }, (_, i) => ({
        version: `0.${7 - i}.0`,
        date: '2026-07-01',
        tagline: '',
        sections: { Добавлено: [`пункт ${i}`] },
      })),
    }),
  },
}))

afterEach(cleanup)

const stats = {
  total: 1340,
  by_category: { meta: 12, empty: 0 },
  by_lifecycle: {},
  by_verdict: {},
  by_kind: {},
  health: { total: 1340, processed: 900, with_notes: 300, notes_base: 900 },
}

describe('SettingsView', () => {
  // Исходный дашборд показывает три последних релиза и прячет остальные за
  // кнопкой. Без неё история обрывается на третьем, и понять, что их больше,
  // нельзя — список выглядит полным.
  it('shows three releases and reveals the rest on demand', async () => {
    render(<SettingsView stats={stats} />)
    expect(await screen.findByText('v0.7.0')).toBeDefined()
    expect(screen.queryByText('v0.4.0')).toBeNull()

    fireEvent.click(screen.getByText(/Показать всю историю/i))
    expect(screen.getByText('v0.4.0')).toBeDefined()
  })
})
