// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'
import { UnreadableBanner } from './UnreadableBanner'

afterEach(cleanup)

describe('UnreadableBanner', () => {
  // Главное требование к полосе — число. Частичные данные, поданные как полные,
  // обманывают тише, чем пустой экран: раньше одна негодная запись гасила все
  // витрины разом, теперь база читается без неё, и молчание об этом было бы
  // заменой громкой поломки на тихую.
  it('называет, сколько показано и сколько всего', () => {
    render(
      <UnreadableBanner
        entries={[{ index: 41, id: 42, reason: 'entry #41 (id=42): unknown status: "ЧТОТО"' }]}
        total={1455}
      />,
    )
    const text = screen.getByText(/Показано/).textContent ?? ''
    expect(text).toContain('1455')
    expect(text).toContain('1456')
  })

  it('на здоровом каталоге молчит', () => {
    const { container } = render(<UnreadableBanner entries={[]} total={1456} />)
    expect(container.textContent).toBe('')
  })

  // Причина нужна тому, кто пойдёт чинить: «запись не прочитана» без причины
  // отправляет искать наугад. Но развёрнутый список по умолчанию свёрнут —
  // полоса висит над всеми вкладками.
  it('показывает причину по требованию', () => {
    render(
      <UnreadableBanner
        entries={[{ index: 41, id: 42, reason: 'unknown status: "ЧТОТО"' }]}
        total={1455}
      />,
    )
    expect(screen.queryByText(/unknown status/)).toBeNull()
    fireEvent.click(screen.getByText(/Показать какие/))
    expect(screen.getByText(/unknown status/)).toBeTruthy()
  })

  // id=0 значит, что и номер прочитать не удалось: тогда единственный адрес
  // записи — её место в файле, и назвать «#0» было бы выдумкой.
  it('запись без разобранного id адресуется местом в файле', () => {
    render(<UnreadableBanner entries={[{ index: 7, id: 0, reason: 'битый id' }]} total={10} />)
    fireEvent.click(screen.getByText(/Показать какие/))
    expect(screen.getByText(/запись №8 в файле/)).toBeTruthy()
  })

  it('склоняет по числу, а не по остатку от пяти', () => {
    const many = Array.from({ length: 11 }, (_, i) => ({ index: i, id: i + 1, reason: 'x' }))
    render(<UnreadableBanner entries={many} total={100} />)
    expect(screen.getByText(/11 записей/)).toBeTruthy()
  })
})
