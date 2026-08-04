// @vitest-environment jsdom
import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'
import { FreshnessBanner } from './FreshnessBanner'
import type { Freshness } from './api'

afterEach(cleanup)

const behind: Freshness = {
  behind: true,
  unknown: false,
  edited_at: '2026-08-04T15:12:00Z',
  facts: [
    { kind: 'catalog', text: 'в каталоге 3 записи после правки страницы', count: 3, ids: [1439, 1440, 1441] },
    { kind: 'version', text: 'база выросла до 0.30.0 (08.08)' },
  ],
  draft: '## 2026-08-10\n\n- каталог: +3\n',
}

// Страница «что в работе сейчас» тухнет тихо: текст остаётся правдоподобным, а
// база вокруг уходит вперёд. Владелец увидел это на своей же странице — в шапке
// стояло «четыре PR смержены», когда их было восемь.
describe('FreshnessBanner', () => {
  it('называет каждую причину отставания и дату правки', () => {
    render(<FreshnessBanner freshness={behind} />)

    expect(screen.getByText(/в каталоге 3 записи/)).toBeDefined()
    expect(screen.getByText(/0\.30\.0/)).toBeDefined()
    // Дата правки — то, от чего отсчитываются все остальные факты; без неё
    // список читается как набор новостей ни о чём.
    expect(screen.getByTestId('freshness-edited').textContent).toContain('04.08')
  })

  it('даёт черновик блока для вставки', () => {
    render(<FreshnessBanner freshness={behind} />)

    expect(screen.getByTestId('freshness-draft').textContent).toContain('## 2026-08-10')
  })

  // Предупреждение, которое горит всегда, перестают читать. Свежая страница
  // молчит — в этом и смысл признака «мир ушёл вперёд» вместо возраста файла.
  it('молчит, когда страница не отстала', () => {
    const fresh: Freshness = { behind: false, unknown: false, facts: [] }
    const { container } = render(<FreshnessBanner freshness={fresh} />)

    expect(container.textContent).toBe('')
  })

  // «Не знаю, когда правили» — не то же самое, что «всё в порядке»: промолчав,
  // страница выглядела бы проверенной, не будучи ею.
  it('признаётся, когда не знает даты правки', () => {
    const unknown: Freshness = { behind: false, unknown: true, facts: [] }
    render(<FreshnessBanner freshness={unknown} />)

    expect(screen.getByTestId('freshness-unknown')).toBeDefined()
  })

  // Отсутствие поля у старой сборки сервера — не повод падать: страница
  // показывает документ, просто без проверки.
  it('переживает отсутствие данных', () => {
    const { container } = render(<FreshnessBanner freshness={undefined} />)

    expect(container.textContent).toBe('')
  })
})
