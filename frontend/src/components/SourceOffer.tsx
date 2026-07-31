/**
 * Предложение исходников по AGPL §13.
 *
 * Условие лицензии касается того, кто пользуется программой ПО СЕТИ: такой
 * пользователь вправе получить её исходники. Когда владелец открыл движок у
 * себя на 127.0.0.1, других пользователей нет — предлагать некому, и строка
 * внизу каждой страницы только шумит. Как только адрес перестаёт быть
 * локальным, ссылка обязана быть на виду.
 *
 * Признак берётся из адреса в браузере, а не с сервера: тот знает, на чём
 * слушает, но не знает, откуда пришёл посетитель, — а важно именно второе.
 */
export function SourceOffer({ hostname = window.location.hostname }: { hostname?: string }) {
  const local = hostname === 'localhost' || hostname === '127.0.0.1' || hostname === '[::1]'
  if (local) return null
  return (
    <footer className="mt-12 border-t border-outline-variant pt-6 text-xs text-on-surface-variant">
      <p>
        kb-engine — свободное ПО под AGPL-3.0-or-later.{' '}
        <a
          className="underline underline-offset-2 hover:text-secondary"
          href="https://github.com/VDV001/kb-engine"
          target="_blank"
          rel="noreferrer"
        >
          Исходный код
        </a>
        .
      </p>
    </footer>
  )
}
