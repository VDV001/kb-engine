// @vitest-environment jsdom
import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'
import { PrivacyToggle } from './PrivacyToggle'

afterEach(cleanup)

// Подпись по-русски, как весь интерфейс: Hidden/Open выбивались (#496).
describe('PrivacyToggle', () => {
  it.each([
    [true, 'Скрыто'],
    [false, 'Видно'],
  ])('masked=%s — «%s»', (masked, label) => {
    render(<PrivacyToggle masked={masked} onChange={() => {}} />)
    expect(screen.getByText(label)).toBeTruthy()
  })
})
