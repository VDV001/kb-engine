import type { Account } from './api'
import { formatRub, toKopecks } from './money'

// Карточка «где лежат деньги». Перенесена из Python-дашборда, но не целиком:
// из старого сайдбара взята только она, потому что месяцы и категории в вебе
// движка уже есть выше по странице, а второй набор тех же фильтров развёл бы
// один экран на два состояния.
//
// Балансы приходят с листа «Счета» и НЕ вычисляются из трат: остаток на карте
// живёт своей жизнью (комиссии, переводы, покупки мимо учёта). Поэтому число
// здесь — подтверждённое человеком, и вместе с ним показывается дата
// подтверждения: без неё карточка выглядит свежей всегда.

// Фирменные цвета банков — те же, что в Python-дашборде: цвет служит
// опознавательным знаком строки, и менять его при переносе значило бы
// заставить владельца заново учить, где какой счёт.
const BANK_COLORS: Record<string, string> = {
  Сбербанк: '#21a038',
  'Альфа-Банк': '#ef3124',
  'Т-Банк': '#ffdd2d',
}

/** Через сколько дней подтверждение баланса считается устаревшим. */
const STALE_AFTER_DAYS = 14

/** Разница в целых днях между двумя датами вида YYYY-MM-DD. */
function daysBetween(from: string, to: string): number {
  const a = Date.parse(from)
  const b = Date.parse(to)
  if (Number.isNaN(a) || Number.isNaN(b)) return 0
  return Math.round((b - a) / 86_400_000)
}

/** Дата подтверждения в том виде, в каком её читает человек: 03.08. */
function shortDate(iso: string): string {
  const m = /^(\d{4})-(\d{2})-(\d{2})/.exec(iso)
  return m ? `${m[3]}.${m[2]}` : iso
}

export function AccountsCard({
  accounts,
  expenses,
  income,
  today,
}: {
  accounts: Account[]
  expenses: string
  income: string
  today: string
}) {
  // Итог складывается из остатков на сейчас, а не из подтверждённых чисел:
  // записал трату — итог обязан уменьшиться, иначе экран показывает вчерашний
  // день и выглядит сломанным.
  const now = (a: Account) => toKopecks(a.current ?? a.balance)
  const total = accounts.reduce((n, a) => n + now(a), 0)

  return (
    <div className="rounded-xl bg-primary-container p-5 text-on-primary">
      <div className="mb-4 flex items-baseline justify-between gap-3">
        <span className="label text-[9px] tracking-[0.2em] opacity-60">На счетах</span>
        <span className="privacy-mask text-xl font-bold" data-testid="accounts-total">
          {formatRub(total)}
        </span>
      </div>

      {accounts.length === 0 ? (
        <p className="label text-[10px] opacity-60">
          Счета не подключены — запустите serve с флагом --from.
        </p>
      ) : (
        <ul className="space-y-2.5">
          {accounts.map((a) => {
            const age = daysBetween(a.updated, today)
            const stale = a.updated !== '' && age > STALE_AFTER_DAYS
            return (
              <li key={a.bank} className="flex items-center gap-2">
                {/* Счёт без фирменного цвета красится текущим цветом текста, а
                    не тоном темы: на тёмной теме фон карточки сам становится
                    тёплым, и точка var(--secondary) в нём растворялась — метка
                    была, но её не было видно. */}
                <span
                  className={`size-2 shrink-0 rounded-full ${BANK_COLORS[a.bank] ? '' : 'bg-current opacity-50'}`}
                  style={BANK_COLORS[a.bank] ? { background: BANK_COLORS[a.bank] } : undefined}
                />
                <span className="label flex-1 text-[10px] opacity-80">{a.bank}</span>
                <span className="flex flex-col items-end">
                  <span className="privacy-mask text-xs font-bold">{formatRub(now(a))}</span>
                  {a.updated !== '' && (
                    <span
                      className={`label text-[8px] ${stale || a.needs_confirmation ? 'opacity-90' : 'opacity-40'}`}
                      data-testid={`confirmed-${a.bank}`}
                      title={
                        `Подтверждён ${formatRub(toKopecks(a.balance))} ${shortDate(a.updated)}` +
                        (a.spent && toKopecks(a.spent) > 0 ? `, после этого списано ${formatRub(toKopecks(a.spent))}` : '')
                      }
                    >
                      {(stale || a.needs_confirmation) && <span data-testid={`stale-${a.bank}`}>⚠ </span>}
                      {shortDate(a.updated)}
                      {a.spent && toKopecks(a.spent) > 0 && (
                        <span className="privacy-mask"> −{formatRub(toKopecks(a.spent))}</span>
                      )}
                    </span>
                  )}
                </span>
              </li>
            )
          })}
        </ul>
      )}

      <div className="mt-4 flex gap-4 border-t border-white/10 pt-3">
        <div>
          <span className="label block text-[8px] tracking-[0.2em] opacity-50">Расходы</span>
          <span className="privacy-mask text-xs font-bold" data-testid="accounts-expenses">
            {formatRub(toKopecks(expenses))}
          </span>
        </div>
        <div>
          <span className="label block text-[8px] tracking-[0.2em] opacity-50">Доходы</span>
          <span className="privacy-mask text-xs font-bold" data-testid="accounts-income">
            {formatRub(toKopecks(income))}
          </span>
        </div>
      </div>
    </div>
  )
}
