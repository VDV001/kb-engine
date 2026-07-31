// @vitest-environment jsdom
import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'
import { SourceOffer } from './SourceOffer'

afterEach(cleanup)

// AGPL §13: у того, кто пользуется движком по сети, должна быть возможность
// получить его исходники. Это не украшение подвала, а условие лицензии, под
// которой движок раздаётся, — поэтому оно закрыто тестом: удалить ссылку
// молча, обновляя вёрстку, не получится.
describe('SourceOffer', () => {
  it('предлагает исходники ссылкой на репозиторий', () => {
    render(<SourceOffer />)
    const link = screen.getByRole('link', { name: /исходн/i })
    expect(link.getAttribute('href')).toBe('https://github.com/VDV001/kb-engine')
  })

  it('называет лицензию', () => {
    render(<SourceOffer />)
    expect(screen.getByText(/AGPL-3\.0/)).toBeDefined()
  })
})
