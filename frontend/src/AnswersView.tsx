import { api } from './api'
import { useResource } from './hooks/useResource'
import { groupByDay, summarise } from './answers'
import { Card, ErrorBox, Section, Spinner } from './components/ui'
import { plural } from './hygiene'

/**
 * Вкладка «Ответы» — журнал того, что агент спрашивал у базы.
 *
 * Заведена не ради статистики, а ради проверяемости: до неё утверждение агента
 * о базе оставалось его пересказом, а проверить, что он вообще открывал базу,
 * было нечем. Счётчик (`kbengine runs`) отвечает «сколько раз», эта вкладка —
 * «о чём и когда».
 *
 * Отбор и сводка живут в answers.ts, здесь только показ.
 */
export function AnswersView({ onAskAgain }: { onAskAgain: (query: string) => void }) {
  const journal = useResource(api.toolCalls)
  if (journal.status === 'loading') return <Spinner />
  if (journal.status === 'failed') return <ErrorBox message={journal.error} />

  const { exists, calls } = journal.data
  const s = summarise(calls)

  if (!exists || calls.length === 0) {
    return (
      <Section title="Ответы" subtitle="Что агент спрашивал у базы через MCP.">
        <Card>
          <p data-testid="answers-empty" className="text-sm text-on-surface-variant">
            {!exists ? (
              <>
                Журнала вызовов нет на диске. Либо MCP-сервер ещё ни разу не отвечал этой
                сборкой, либо он старше журнала — тогда помогает пересборка (<code>kbup</code>).
                Пустой список здесь означал бы, что агент базу не спрашивал, а это другое.
              </>
            ) : (
              <>
                Журнал заведён, но ни одного вызова в нём нет — агент базу пока не спрашивал.
              </>
            )}
          </p>
        </Card>
      </Section>
    )
  }

  const days = groupByDay(calls)
  return (
    <Section
      title="Ответы"
      subtitle={`Что агент спрашивал у базы через MCP: ${s.total} ${plural(s.total, ['вызов', 'вызова', 'вызовов'])} за ${s.days} ${plural(s.days, ['день', 'дня', 'дней'])}.`}
    >
      <div className="grid grid-cols-2 gap-4 sm:grid-cols-4">
        {/* Подписи склоняются вместе с числом: «1 дней в журнале» — та же
            небрежность, что «11 записи», и видна она сразу, а не в тестах. */}
        <Stat label={plural(s.total, ['вызов', 'вызова', 'вызовов'])} value={s.total} />
        <Stat label={plural(s.searches, ['поиск', 'поиска', 'поисков'])} value={s.searches} />
        <Stat label="без ответа" value={s.failed} testId="answers-failed" />
        <Stat
          label={`${plural(s.days, ['день', 'дня', 'дней'])} в журнале`}
          value={s.days}
        />
      </div>

      {days.map((d) => (
        <div key={d.day} className="space-y-2">
          <h3 data-testid="answers-day" className="label text-on-surface-variant">
            {d.day}
          </h3>
          <Card className="divide-y divide-outline-variant p-0">
            {d.calls.map((c, i) => (
              <div key={`${c.at}-${i}`} className="flex flex-wrap items-baseline gap-x-3 gap-y-1 px-4 py-2.5">
                <span className="font-mono text-xs tabular-nums text-on-surface-variant">
                  {c.at.slice(11, 16)}
                </span>
                <span className="label text-on-surface-variant">{c.tool}</span>
                {c.query ? (
                  // Запрос кликабелен: журнал отвечает «о чём спрашивали», и
                  // естественное следующее действие — посмотреть тот же ответ
                  // самому, в архиве, по первичным данным.
                  <button
                    type="button"
                    onClick={() => onAskAgain(c.query ?? '')}
                    className="min-w-0 truncate text-sm text-on-surface underline decoration-outline-variant hover:decoration-current"
                  >
                    {c.query}
                  </button>
                ) : (
                  <span className="text-sm text-on-surface-variant">— (сводка, спрашивать нечего)</span>
                )}
                {!c.ok && (
                  <span className="label ml-auto rounded bg-tag-bg-4 px-2 py-0.5 text-tag-text-4">
                    без ответа
                  </span>
                )}
              </div>
            ))}
          </Card>
        </div>
      ))}

      {/* Правило 11: вкладка называет, чего она НЕ знает. Без этого абзаца
          «43 вызова» читается как «43 раза я спросил базу» и как «43 раза я
          сходил проверить ответ», а верно ни то, ни другое. */}
      <Card>
        <p className="text-sm text-on-surface-variant">
          Чего этот журнал не знает: сколько раз человек перешёл по ссылке на витрину —
          переход видит браузер, а не движок; сколько вопросов задал сам владелец — на один
          вопрос агент нередко зовёт базу несколько раз; и что было до первой записи журнала —
          старее себя он не помнит.
        </p>
      </Card>
    </Section>
  )
}

function Stat({ label, value, testId }: { label: string; value: number; testId?: string }) {
  return (
    <Card>
      <p data-testid={testId} className="font-headline text-2xl tabular-nums">
        {value}
      </p>
      <p className="label text-on-surface-variant">{label}</p>
    </Card>
  )
}
