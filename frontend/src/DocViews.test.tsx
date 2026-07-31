// @vitest-environment jsdom
import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'
import type { Document } from './api'
import { DocumentView } from './DocViews'

afterEach(cleanup)

const team: Document = {
  label: 'IT-отдел',
  title: 'Команда',
  sections: [
    {
      title: 'Состав',
      cards: [
        { title: 'Кирилл', meta: 'Тим-лид и тех-лид', badge: 'работает', body: 'Формальный лидер разработки' },
        { title: 'Ваня', meta: 'DevOps', badge: 'уходит', body: 'Компетенция уходит из компании целиком' },
      ],
    },
  ],
}

describe('DocumentView: три состояния вместо одного', () => {
  // Совет «добавьте флаг» верен ровно в одном случае из трёх. На живом запуске
  // флаг --team стоял, файл существовал, но был markdown вместо JSON — и экран
  // всё равно советовал добавить флаг. Отправлять человека чинить то, что у
  // него уже сделано, хуже, чем не сказать ничего.
  it('без файла советует флаг', () => {
    render(<DocumentView load={async () => null} name="Team" />)
    return screen.findByText(/не настроен/i).then((el) => {
      expect(el.textContent).toMatch(/--team/)
    })
  })

  it('нераспознанный файл говорит про формат, а не про флаг', async () => {
    render(
      <DocumentView
        load={async () => {
          throw new Error('document is not valid JSON')
        }}
        name="Team"
      />,
    )
    const el = await screen.findByText(/не разобран|не JSON|формат/i)
    expect(el.textContent).not.toMatch(/запустите serve/i)
  })

  it('прочая ошибка показывает саму ошибку', async () => {
    render(
      <DocumentView
        load={async () => {
          throw new Error('open team.json: permission denied')
        }}
        name="Team"
      />,
    )
    expect(await screen.findByText(/permission denied/i)).toBeDefined()
  })
})

describe('DocumentView: маска', () => {
  // Состав отдела — данные о третьих лицах: кто уходит, кто на чём держится.
  // Маска по умолчанию включена по той же причине, что и в финансах: безопасное
  // состояние — то, в котором оказываешься, а не то, которое надо не забыть
  // включить. Скрываются имена и заметки, роли остаются: без ролей страница
  // перестаёт что-либо значить, а без имён — нет.
  it('скрывает имена и заметки, оставляя роли и статусы', async () => {
    render(<DocumentView load={async () => team} name="Team" masked />)
    expect(await screen.findByText('Тим-лид и тех-лид')).toBeDefined()
    expect(screen.getByText('уходит')).toBeDefined()
    expect(screen.queryByText('Кирилл')).toBeNull()
    expect(screen.queryByText(/Формальный лидер/)).toBeNull()
  })

  it('без маски показывает всё', async () => {
    render(<DocumentView load={async () => team} name="Team" />)
    expect(await screen.findByText('Кирилл')).toBeDefined()
    expect(screen.getByText(/Формальный лидер/)).toBeDefined()
  })
})
