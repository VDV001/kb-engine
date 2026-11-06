import type { Transaction, TransactionAddRequest } from './api'

// Форма операции (#435) без React: что уходит на сервер, как назвать отказ и
// что подсказать в полях. Правила записи — у сервера (fin add), здесь их нет.

export interface AddForm {
  kind: 'expense' | 'income'
  date: string
  amount: string
  currency: string
  category: string
  subcategory: string
  place: string
  description: string
  source: string
  account: string
}

export function emptyAddForm(today: string): AddForm {
  return {
    kind: 'expense', date: today, amount: '', currency: '', category: '', subcategory: '',
    place: '', description: '', source: '', account: '',
  }
}

/**
 * Запрос из полей формы. У траты — категория, подкатегория и место, у дохода —
 * источник: поле другого вида, оставшееся после переключения, не уходит.
 * Пустые поля не отправляются: пусто у сервера значит «по умолчанию».
 */
export function toAddRequest(f: AddForm, tz: string, force = false): TransactionAddRequest {
  const own: (keyof AddForm)[] =
    f.kind === 'expense'
      ? ['date', 'amount', 'currency', 'category', 'subcategory', 'place', 'description', 'account']
      : ['date', 'amount', 'currency', 'source', 'description', 'account']
  const req: TransactionAddRequest = { kind: f.kind, amount: '' }
  for (const k of own) {
    const v = f[k].trim()
    if (v) (req as unknown as Record<string, string>)[k] = v
  }
  if (tz) req.tz = tz
  if (force) req.force = true
  return req
}

export type AddFailureKind = 'repeat' | 'unsupported' | 'bad' | 'server'

/** Отказ записи словами человека: текст сервера уже такой, кроме 404/405 и 500. */
export function addFailure(status: number, text: string): { kind: AddFailureKind; message: string } {
  if (status === 409) {
    const found = repeatOf(text)
    return { kind: 'repeat', message: `Такая запись уже есть${found ? `: ${found}` : ''}. Если это вторая такая же операция — «Все равно записать»` }
  }
  if (status === 404 || status === 405) {
    return { kind: 'unsupported', message: 'Этот сервер не принимает запись: он запущен без журнала на запись' }
  }
  if (status >= 400 && status < 500) return { kind: 'bad', message: text || `сервер отказал (${status})` }
  if (status === 500 || !text) return { kind: 'server', message: 'Сервер не записал операцию — попробуйте еще раз; причина в журнале сервера' }
  return { kind: 'server', message: text }
}

/**
 * Найденная запись из ответа 409. Текст сервера общий с fin add: в нем
 * подсказка флага --force и id записи — человеку в форме они не нужны.
 */
function repeatOf(text: string): string {
  return text
    .replace(/^repeat:\s*/, '')
    .replace(/^.*?уже есть:?\s*/i, '')
    .replace(/\s*—\s*повторить осознанно.*$/s, '')
    .replace(/\s*\([0-9A-Z]{26}\)/, '')
    .replace(/[\s·]+$/, '')
}

/** Подсказки полей из журнала: частые выше, пустые и повторы отброшены. */
export function suggestions(txs: Transaction[]) {
  const rank = (pick: (t: Transaction) => string | undefined) => {
    const n = new Map<string, number>()
    for (const t of txs) {
      const v = pick(t)?.trim()
      if (v) n.set(v, (n.get(v) ?? 0) + 1)
    }
    return [...n].sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0])).map(([v]) => v)
  }
  return {
    categories: rank((t) => t.category),
    subcategories: rank((t) => t.subcategory),
    places: rank((t) => t.place),
    sources: rank((t) => t.source),
    currencies: rank((t) => t.currency),
  }
}
