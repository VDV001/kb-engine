import { describe, expect, it } from 'vitest'
import { artefactHref, cheatsheetsOf, opensInBrowser } from './artefacts'

const entry = (over: Record<string, unknown> = {}) =>
  ({ id: 1, title: 'x', category: 'creations', tags: [], ...over }) as never

describe('артефакты базы', () => {
  // Файл отдаёт маршрут /kb/, и список разрешённого — сам каталог: путь, не
  // названный ни одной записью, наружу не уходит.
  it('строит адрес артефакта от корня витрины', () => {
    expect(artefactHref('creations/cheatsheets/x/y.html')).toBe('/kb/creations/cheatsheets/x/y.html')
  })

  // У большинства записей файла нет вовсе — они ссылаются на чужую статью.
  it('без файла адреса нет', () => {
    expect(artefactHref(undefined)).toBe('')
    expect(artefactHref('')).toBe('')
  })

  // Пробелы и кириллица в пути законны: имена папок писал человек.
  it('кодирует путь, но не съедает разделители', () => {
    expect(artefactHref('creations/мои файлы/шпора.html')).toBe(
      '/kb/creations/%D0%BC%D0%BE%D0%B8%20%D1%84%D0%B0%D0%B9%D0%BB%D1%8B/%D1%88%D0%BF%D0%BE%D1%80%D0%B0.html',
    )
  })

  // Признак шпаргалки живёт в ДАННЫХ — тег, который проставлен всем пяти. Знать
  // про структуру папок фронту незачем: перенос файла не должен ломать вкладку.
  it('отбирает шпаргалки по тегу, а не по пути', () => {
    const list = [
      entry({ id: 1, tags: ['cheatsheet', 'tui'], file: 'creations/cheatsheets/a/a.html' }),
      entry({ id: 2, tags: ['habr'], file: 'creations/habr/b/b.html' }),
      entry({ id: 3, tags: ['cheatsheet'], file: 'куда-то/ещё/c.html' }),
    ]
    expect(cheatsheetsOf(list).map((e) => e.id)).toEqual([1, 3])
  })

  // Запись с тегом, но без файла открыть нечем: показывать плитку, которая
  // никуда не ведёт, значит обещать больше, чем есть.
  it('шпаргалку без файла не показывает', () => {
    expect(cheatsheetsOf([entry({ id: 9, tags: ['cheatsheet'] })])).toEqual([])
  })

  it('свежие идут первыми — шпаргалку читают ту, что обновляли', () => {
    const list = [
      entry({ id: 1, tags: ['cheatsheet'], file: 'a.html', date_added: '2026-07-05' }),
      entry({ id: 2, tags: ['cheatsheet'], file: 'b.html', date_added: '2026-08-02' }),
    ]
    expect(cheatsheetsOf(list).map((e) => e.id)).toEqual([2, 1])
  })

  // Движок отдаёт markdown как text/markdown, и браузер такой тип СКАЧИВАЕТ, а
  // не показывает. Кнопка «открыть», после которой падает файл в загрузки,
  // обещает больше, чем делает, поэтому её там нет.
  it('открывается в браузере только то, что браузер покажет', () => {
    expect(opensInBrowser('creations/cheatsheets/a/a.html')).toBe(true)
    expect(opensInBrowser('creations/x/y.HTML')).toBe(true)
    expect(opensInBrowser('notes/2026-03-26_future-plans_v1.md')).toBe(false)
    expect(opensInBrowser('standards/memory-architecture/v1.md')).toBe(false)
    expect(opensInBrowser(undefined)).toBe(false)
  })
})
