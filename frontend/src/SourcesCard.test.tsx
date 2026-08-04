// @vitest-environment jsdom
import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'
import { SourcesCard } from './SourcesCard'
import type { SourceState } from './api'

afterEach(cleanup)

const sources: SourceState[] = [
  { name: 'Now', flag: '--now', edited_at: '2026-08-04T15:42:00Z', behind: false, unknown: false, no_anchors: false, age_days: 0, facts: [] },
  {
    name: 'Projects', flag: '--projects', edited_at: '2026-07-31T10:00:00Z',
    behind: true, unknown: false, no_anchors: false, age_days: 3,
    facts: [{ kind: 'version-mention', text: 'страница называет kb-engine v0.5.0, сейчас 0.15.0' }],
  },
  { name: 'Team', flag: '--team', edited_at: '2026-08-01T10:00:00Z', behind: false, unknown: false, no_anchors: true, age_days: 3, facts: [] },
]

// Страницы Team и Projects тухнут так же, как Now, но смотрят на них реже —
// значит врут дольше. Сводка нужна затем, чтобы это было видно, не открывая
// каждую по очереди.
describe('SourcesCard', () => {
  it('называет причину у отставшей страницы', () => {
    render(<SourcesCard sources={sources} />)

    expect(screen.getByText(/kb-engine v0\.5\.0/)).toBeDefined()
    expect(screen.getByTestId('source-Projects').textContent).toContain('отстала')
  })

  it('различает «всё сошлось» и «сверять не с чем»', () => {
    render(<SourcesCard sources={sources} />)

    // У Now опоры есть и находок нет — это проверено.
    expect(screen.getByTestId('source-Now').textContent).toContain('свежая')
    // У Team опор нет вовсе, и зелёная галочка тут означала бы неправду.
    expect(screen.getByTestId('source-Team').textContent).toContain('сверять не с чем')
  })

  it('показывает возраст даже там, где он не приговор', () => {
    render(<SourcesCard sources={sources} />)

    expect(screen.getByTestId('source-Team').textContent).toMatch(/3\s*дн/)
  })

  // Источники не настроены — блока нет вовсе, а не пустая рамка: пустота
  // читается как «всё в порядке», хотя проверять было нечего.
  it('молчит, когда источников нет', () => {
    const { container } = render(<SourcesCard sources={[]} />)

    expect(container.textContent).toBe('')
  })
})
