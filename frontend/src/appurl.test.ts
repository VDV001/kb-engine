import { describe, expect, it } from 'vitest'
import { appURL, localURL } from './appurl'

// Корень — каталог приложения, а не страницы: в окне DVK витрина под /app/,
// под kbengine serve — в корне. Слэш в начале не уводит из-под префикса.
describe('адреса приложения', () => {
  it.each([
    ['https://dvk.local/app/', 'api/stats', 'https://dvk.local/app/api/stats'],
    ['https://dvk.local/app/', '/api/stats', 'https://dvk.local/app/api/stats'],
    ['https://dvk.local/app/', 'kb/a%20b.html', 'https://dvk.local/app/kb/a%20b.html'],
    ['http://127.0.0.1:8080/', 'api/search?q=x', 'http://127.0.0.1:8080/api/search?q=x'],
  ])('от %s путь %s', (root, path, want) => {
    expect(appURL(path, root)).toBe(want)
  })

  // Картинки проектов в projects.json записаны как /media/…: локальный путь
  // уходит под префикс, чужой адрес не трогается.
  it.each([
    ['/media/x.png', 'https://dvk.local/app/media/x.png'],
    ['https://cdn.example/x.png', 'https://cdn.example/x.png'],
  ])('картинка %s', (src, want) => {
    expect(localURL(src, 'https://dvk.local/app/')).toBe(want)
  })

  // По умолчанию корень — над каталогом модуля (assets/ в сборке), поэтому
  // адрес страницы (/app без слэша, /now/) на него не влияет.
  it('корень по умолчанию — от модуля, а не от страницы', () => {
    expect(appURL('api/stats')).toBe(new URL('../api/stats', new URL('./appurl.ts', import.meta.url)).href)
  })
})
