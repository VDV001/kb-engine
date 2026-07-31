// @vitest-environment jsdom
import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'
import { SourceOffer } from './SourceOffer'

afterEach(cleanup)

// AGPL §13 обязывает предложить исходники тому, кто пользуется программой
// ПО СЕТИ. Когда владелец открыл движок у себя на 127.0.0.1, других
// пользователей нет и предлагать некому — строка внизу каждой страницы там
// только шумит. Как только адрес перестаёт быть локальным, условие лицензии
// вступает в силу и ссылка обязана быть на виду.
describe('SourceOffer', () => {
  it('молчит на локальном адресе', () => {
    const { container } = render(<SourceOffer hostname="127.0.0.1" />)
    expect(container.firstChild).toBeNull()
    cleanup()
    expect(render(<SourceOffer hostname="localhost" />).container.firstChild).toBeNull()
  })

  it('предлагает исходники, когда движок доступен по сети', () => {
    render(<SourceOffer hostname="kb.example.com" />)
    const link = screen.getByRole('link', { name: /исходн/i })
    expect(link.getAttribute('href')).toBe('https://github.com/VDV001/kb-engine')
    expect(screen.getByText(/AGPL-3\.0/)).toBeDefined()
  })

  // IP в локальной сети — это уже «по сети»: на движок смотрит кто-то ещё.
  it('считает адрес в локальной сети сетевым', () => {
    render(<SourceOffer hostname="192.168.1.10" />)
    expect(screen.getByRole('link', { name: /исходн/i })).toBeDefined()
  })
})
