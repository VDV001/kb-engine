import { useMemo, useState } from 'react'
import { AddError, api } from './api'
import type { Account, Transaction, TransactionAdded } from './api'
import { addFailure, emptyAddForm, suggestions, toAddRequest } from './financeAdd'
import type { AddForm } from './financeAdd'

const FIELD_NAME: Record<string, string> = {
  place: 'место', category: 'категория', subcategory: 'подкатегория', source: 'источник', account: 'счет',
}

const inputClass = 'label w-full rounded border border-outline-variant bg-surface px-3 py-1.5 text-xs'

/**
 * Форма операции в «Финансах» (#435): то же, что kbengine fin add. Подсказки
 * полей — из журнала, правила записи — у сервера. «Такая запись уже есть» —
 * не ошибка, а вопрос: повтор пишется только по явной кнопке.
 */
export function AddTransactionForm({
  transactions,
  accounts,
  today,
  onAdded,
}: {
  transactions: Transaction[]
  accounts: Account[]
  today: string
  onAdded: (added: TransactionAdded) => void
}) {
  const [form, setForm] = useState<AddForm>(() => emptyAddForm(today))
  const [busy, setBusy] = useState(false)
  const [failure, setFailure] = useState<ReturnType<typeof addFailure> | null>(null)
  const [done, setDone] = useState<TransactionAdded | null>(null)
  const hints = useMemo(() => suggestions(transactions), [transactions])
  const set = (k: keyof AddForm) => (e: { target: { value: string } }) => {
    setForm((f) => ({ ...f, [k]: e.target.value }))
    setFailure(null)
  }

  async function submit(force: boolean) {
    if (!form.amount.trim()) {
      setFailure({ kind: 'bad', message: 'Введите сумму' })
      return
    }
    setBusy(true)
    setDone(null)
    try {
      const tz = Intl.DateTimeFormat().resolvedOptions().timeZone
      const added = await api.addTransaction(toAddRequest(form, tz, force))
      setDone(added)
      setFailure(null)
      // Дата, вид, счет и валюта остаются: подряд обычно вносят несколько
      // операций одного дня с одного счета.
      setForm((f) => ({ ...f, amount: '', place: '', description: '', subcategory: '' }))
      onAdded(added)
    } catch (e) {
      setFailure(e instanceof AddError ? addFailure(e.status, e.text) : { kind: 'server', message: String(e) })
    } finally {
      setBusy(false)
    }
  }

  const field = (k: keyof AddForm, label: string, list?: string[], type = 'text', placeholder?: string) => (
    <label className="block">
      <span className="label mb-1 block text-[10px] opacity-60">{label}</span>
      <input
        type={type}
        value={form[k]}
        onChange={set(k)}
        list={list?.length ? `add-${k}` : undefined}
        inputMode={k === 'amount' ? 'decimal' : undefined}
        placeholder={placeholder}
        className={inputClass}
      />
      {list && list.length > 0 && (
        <datalist id={`add-${k}`}>
          {list.slice(0, 50).map((v) => (
            <option key={v} value={v} />
          ))}
        </datalist>
      )}
    </label>
  )

  return (
    <form
      className="mb-6 rounded-lg bg-surface-low p-4"
      onSubmit={(e) => {
        e.preventDefault()
        void submit(false)
      }}
    >
      <div className="mb-4 flex gap-2" role="radiogroup" aria-label="Вид операции">
        {(['expense', 'income'] as const).map((k) => (
          <button
            key={k}
            type="button"
            role="radio"
            aria-checked={form.kind === k}
            onClick={() => setForm((f) => ({ ...f, kind: k }))}
            className={`label rounded-full px-3 py-1 text-xs ${
              form.kind === k ? 'bg-secondary text-white' : 'bg-surface-high text-on-surface-variant'
            }`}
          >
            {k === 'expense' ? 'Расход' : 'Доход'}
          </button>
        ))}
      </div>
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-4">
        {field('amount', 'Сумма')}
        {field('date', 'Дата', undefined, 'date')}
        {field('currency', 'Валюта', hints.currencies, 'text', 'по счету')}
        {field('account', 'Счет', accounts.map((a) => a.bank))}
        {form.kind === 'expense' ? (
          <>
            {field('category', 'Категория', hints.categories)}
            {field('subcategory', 'Подкатегория', hints.subcategories)}
            {field('place', 'Место', hints.places)}
          </>
        ) : (
          field('source', 'Источник', hints.sources)
        )}
        {field('description', 'Описание')}
      </div>
      <div className="mt-4 flex flex-wrap items-center gap-3">
        <button type="submit" disabled={busy} className="label bg-primary px-4 py-2 text-xs text-on-primary disabled:opacity-50">
          {busy ? 'Записываю…' : 'Записать'}
        </button>
        {failure?.kind === 'repeat' && (
          <button
            type="button"
            disabled={busy}
            onClick={() => void submit(true)}
            className="label bg-surface-high px-4 py-2 text-xs disabled:opacity-50"
          >
            Все равно записать
          </button>
        )}
        {failure && (
          <p role="alert" className="label text-xs text-error">
            {failure.message}
          </p>
        )}
        {done && !failure && (
          <p role="status" className="label text-xs text-on-surface-variant">
            Записано: {done.date} · <span className="privacy-mask">{done.amount}</span>
            {done.corrections?.map((c) => (
              <span key={c.field}>
                {' '}· {FIELD_NAME[c.field] ?? c.field}: «{c.typed}» → «{c.used}»
              </span>
            ))}
            {done.warnings?.map((w) => (
              <span key={w} className="block">
                {w}
              </span>
            ))}
          </p>
        )}
      </div>
    </form>
  )
}
