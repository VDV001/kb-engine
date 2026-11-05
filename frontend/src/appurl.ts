// Адреса запросов и артефактов считаются от корня приложения, а не от адреса
// страницы. Окно DVK отдает витрину под /app/ и проксирует /app/api/* на
// kb-сервер с токеном (план G); под kbengine serve витрина в корне. Адрес
// страницы ненадежен: /app без слэша и глубокие адреса SPA (/now/) увели бы
// относительный путь мимо прокси (ревью #430). Корень — каталог над модулем:
// в сборке весь код лежит в <корень>/assets/, в vite dev модуль — /src/appurl.ts.
const ROOT = new URL('../', import.meta.url)

/** Полный адрес пути приложения (api/…, kb/…); ведущий «/» не уводит из-под префикса. */
export function appURL(path: string, root: URL | string = ROOT): string {
  return new URL(path.replace(/^\/+/, ''), root).href
}

/**
 * Адрес картинки из данных: локальный путь (/media/…, как пишет projects.json)
 * — под корень приложения, чужой адрес — как есть.
 */
export function localURL(src: string, root: URL | string = ROOT): string {
  return src.startsWith('/') && !src.startsWith('//') ? appURL(src, root) : src
}
