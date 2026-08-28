// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { SelectionBar } from './components/SelectionBar'

afterEach(cleanup)

// Ссылка из ответа агента открывала обычный поиск: на экране был список без
// единого признака того, что это ОТВЕТ НА ВОПРОС и что за ним стоит ещё
// полторы тысячи записей. Полоса отвечает ровно на это.
describe('SelectionBar', () => {
  it('называет запрос и оба числа', () => {
    render(
      <SelectionBar
        selection={{ query: 'ddd', shown: 80, total: 1573, fromAgent: false }}
        onReset={() => {}}
      />,
    )
    const text = screen.getByTestId('selection-bar').textContent ?? ''
    expect(text).toContain('ddd')
    expect(text).toContain('80')
    expect(text).toContain('1573')
  })

  it('ссылку агента называет ответом на вопрос, свой поиск — нет', () => {
    render(
      <SelectionBar
        selection={{ query: 'ddd', shown: 80, total: 1573, fromAgent: true }}
        onReset={() => {}}
      />,
    )
    expect(screen.getByTestId('selection-bar').textContent).toMatch(/агент/i)
  })

  // Пока счёт неизвестен, число не выдумывается и не подставляется прошлое:
  // «нашлось 1573» на летящем запросе — ровно то враньё, ради которого полосу
  // и заводили.
  it('на летящем запросе числа не называет', () => {
    render(
      <SelectionBar
        selection={{ query: 'ddd', shown: null, total: 1573, fromAgent: false }}
        onReset={() => {}}
      />,
    )
    expect(screen.getByTestId('selection-bar').textContent).not.toContain('80')
  })

  it('кнопка сброса возвращает весь каталог', () => {
    const onReset = vi.fn()
    render(
      <SelectionBar
        selection={{ query: 'ddd', shown: 80, total: 1573, fromAgent: true }}
        onReset={onReset}
      />,
    )
    fireEvent.click(screen.getByRole('button', { name: /все записи|показать все|сброс/i }))
    expect(onReset).toHaveBeenCalledTimes(1)
  })

  it('без выборки полосы нет вовсе', () => {
    const { container } = render(<SelectionBar selection={null} onReset={() => {}} />)
    expect(container.innerHTML).toBe('')
  })
})
