// Условие AGPL §13, а не подпись в подвале: тот, кто пользуется движком по
// сети, вправе получить его исходники, и предложение должно быть на виду.
// Поэтому ссылка живёт в общем каркасе и видна на каждой вкладке.
export function SourceOffer() {
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
