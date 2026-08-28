// @vitest-environment jsdom
import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'
import { useInitialUrlState } from './useInitialUrlState'

afterEach(cleanup)

function Probe() {
  const s = useInitialUrlState()
  return (
    <span data-testid="v">
      {s.src ?? 'нет'}|{s.q ?? ''}
    </span>
  )
}

// ⚠️ Дефект, найденный НА ЭКРАНЕ, а не тестом: разбор адреса стоял прямо в теле
// App и повторялся на каждом рендере, а useUrlSync к тому моменту уже переписал
// адрес на себя — без src. Отметка «ответ агента» жила ровно один кадр.
//
// Ни один тест это не ловил: в тестах вида linkedQuery приходил пропсом уже
// готовым, а связка в App была единственным местом без проверки.
describe('состояние адреса на входе', () => {
  it('переживает переписывание адреса витриной', () => {
    window.history.replaceState(null, '', '/?tab=archives&q=ddd&src=mcp')
    const { rerender } = render(<Probe />)
    expect(screen.getByTestId('v').textContent).toBe('mcp|ddd')

    // Ровно то, что делает useUrlSync через миг после монтирования.
    window.history.replaceState(null, '', '/?tab=archives&q=ddd')
    rerender(<Probe />)

    expect(screen.getByTestId('v').textContent).toBe('mcp|ddd')
  })

  // Отрицательный контроль: новый заход по адресу без отметки её не выдумывает.
  it('на своём поиске отметки нет', () => {
    window.history.replaceState(null, '', '/?tab=archives&q=ddd')
    render(<Probe />)
    expect(screen.getByTestId('v').textContent).toBe('нет|ddd')
  })
})
