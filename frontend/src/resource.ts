// Три состояния асинхронной загрузки, вынесенные из компонентов.
//
// До этого каждый из четырёх грузящих файлов держал свою пару useState +
// useEffect и свою трактовку ошибки, а `null` в двух из них означал сразу и
// «сервер вернул null» (вид не настроен), и «запрос упал». Тип разводит эти
// случаи; кто их рендерит одинаково — делает это явно, а не по совпадению.
export type Resource<T> =
  | { status: 'loading' }
  | { status: 'ready'; data: T }
  | { status: 'failed'; error: string }

/**
 * Человекочитаемый текст ошибки из чего угодно, что прилетело в catch.
 *
 * Гарантия — непустая строка: пустой ErrorBox не сообщает пользователю
 * ничего, а `new Error('')` и `String('')` дают именно его.
 */
export function errorMessage(e: unknown): string {
  if (e instanceof Error) return e.message || e.name || 'Unknown error'
  if (typeof e === 'string') return e || 'Unknown error'
  return String(e)
}
