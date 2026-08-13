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

// Стрелка в имени счёта — соглашение книги: «Резерв → Наличные», «Долг →
// Отец». Она значит «одно из нескольких одного рода», и витрина читает её,
// чтобы не складывать в одно число деньги на карте, деньги отложенные и
// деньги, которых сейчас нет, потому что их занял человек.
//
// Разбирает имя движок — он присылает group и name_in_group готовыми. Правило
// живёт в домене в одном экземпляре, и страница его не переоткрывает.
//
// Разбор здесь — запасной путь ровно для одного случая: старая сборка сервера,
// которая полей ещё не отдаёт. Тогда карточка покажет то же самое, а не
// свалится в одну кучу, — но источником остаётся сервер.
const GROUP_SEPARATOR = '→'

function splitAccountName(a: Account): { group: string; name: string } {
  if (a.group !== undefined || a.name_in_group !== undefined) {
    return { group: a.group ?? '', name: a.name_in_group ?? a.bank }
  }
  const at = a.bank.indexOf(GROUP_SEPARATOR)
  if (at < 0) return { group: '', name: a.bank.trim() }
  return {
    group: a.bank.slice(0, at).trim(),
    name: a.bank.slice(at + GROUP_SEPARATOR.length).trim(),
  }
}

export function AccountsCard({
  accounts,
  expenses,
  income,
  today,
  transfersExcluded = 0,
}: {
  accounts: Account[]
  expenses: string
  income: string
  today: string
  /**
   * Сколько переводов между своими счетами исключено из итогов периода. Нужен
   * не сам счёт, а повод сказать вслух: у перевода видна только уходящая
   * сторона, поэтому расчётный остаток на неё занижен. Ноль — молчим.
   */
  transfersExcluded?: number
}) {
  // Итог складывается из остатков на сейчас, а не из подтверждённых чисел:
  // записал трату — итог обязан уменьшиться, иначе экран показывает вчерашний
  // день и выглядит сломанным.
  const now = (a: Account) => toKopecks(a.current ?? a.balance)
  const total = accounts.reduce((n, a) => n + now(a), 0)

  // Порядок групп — тот, в котором счета стоят в книге: владелец сам решил, что
  // за чем идёт на листе, и витрине незачем это переставлять.
  const groups: { group: string; accounts: Account[] }[] = []
  for (const a of accounts) {
    const { group } = splitAccountName(a)
    const found = groups.find((g) => g.group === group)
    if (found) found.accounts.push(a)
    else groups.push({ group, accounts: [a] })
  }
  const plain = groups.find((g) => g.group === '')?.accounts ?? []
  const kinds = groups.filter((g) => g.group !== '')
  const free = plain.reduce((n, a) => n + now(a), 0)

  return (
    <div className="rounded-xl bg-primary-container p-5 text-on-primary">
      <div className="mb-4 flex items-baseline justify-between gap-3">
        <span className="label text-[9px] tracking-[0.2em] opacity-60">На счетах</span>
        <span className="flex flex-col items-end">
          <span className="privacy-mask text-xl font-bold" data-testid="accounts-total">
            {formatRub(total)}
          </span>
          {/* Расшифровка появляется только когда есть что расшифровывать:
              «свободно 1 000» под итогом «1 000» объясняет то, чего не
              происходит, и учит не читать эту строку. */}
          {kinds.length > 0 && (
            <span className="label text-[8px] opacity-60" data-testid="accounts-free">
              свободно <span className="privacy-mask font-bold">{formatRub(free)}</span>
            </span>
          )}
        </span>
      </div>

      {accounts.length === 0 ? (
        <p className="label text-[10px] opacity-60">
          Счета не подключены — запустите serve с флагом --from.
        </p>
      ) : (
        <>
          <ul className="space-y-2.5">
            {plain.map((a) => (
              <AccountRow key={a.bank} account={a} label={a.bank} today={today} />
            ))}
          </ul>

          {kinds.map((k) => (
            <div key={k.group} className="mt-4 border-t border-white/10 pt-3">
              <div className="mb-2 flex items-baseline justify-between gap-3">
                <span
                  className="label text-[8px] tracking-[0.2em] opacity-50"
                  data-testid={`group-${k.group}`}
                >
                  {k.group}
                </span>
                <span
                  className="privacy-mask text-xs font-bold opacity-80"
                  data-testid={`group-total-${k.group}`}
                >
                  {formatRub(k.accounts.reduce((n, a) => n + now(a), 0))}
                </span>
              </div>
              <ul className="space-y-2.5">
                {k.accounts.map((a) => (
                  <AccountRow
                    key={a.bank}
                    account={a}
                    label={splitAccountName(a).name}
                    today={today}
                  />
                ))}
              </ul>
            </div>
          ))}
        </>
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

      {/* Перевод себе уходит со счёта и приходит на другой, но домен не даёт
          доходу счёта — значит движок видит только уходящую сторону, и
          расчётный остаток занижен на неё. Названо здесь, рядом с числом, а не
          в документации: расхождение с банком человек замечает именно тут. */}
      {transfersExcluded > 0 && (
        <p className="label mt-3 text-[9px] leading-relaxed opacity-60" data-testid="accounts-transfers-note">
          Переводы себе занижают расчёт: приход второй стороны движку не виден.
        </p>
      )}
    </div>
  )
}

/**
 * Одна строка счёта: точка цвета, имя и остаток на сейчас с датой подтверждения.
 *
 * Имя приходит параметром, а не берётся из счёта: внутри группы слово «Долг»
 * уже стоит заголовком, и повторять его в каждой строке значит тратить ширину
 * на то, что человек только что прочитал.
 */
function AccountRow({
  account: a,
  label,
  today,
}: {
  account: Account
  label: string
  today: string
}) {
  const now = toKopecks(a.current ?? a.balance)
  const age = daysBetween(a.updated, today)
  const stale = a.updated !== '' && age > STALE_AFTER_DAYS
  const spent = a.spent ? toKopecks(a.spent) : 0

  return (
    <li className="flex items-center gap-2">
      {/* Счёт без фирменного цвета красится текущим цветом текста, а не тоном
          темы: на тёмной теме фон карточки сам становится тёплым, и точка
          var(--secondary) в нём растворялась — метка была, но её не было видно. */}
      <span
        className={`size-2 shrink-0 rounded-full ${BANK_COLORS[a.bank] ? '' : 'bg-current opacity-50'}`}
        style={BANK_COLORS[a.bank] ? { background: BANK_COLORS[a.bank] } : undefined}
      />
      <span className="label flex-1 text-[10px] opacity-80">{label}</span>
      <span className="flex flex-col items-end">
        <span className="privacy-mask text-xs font-bold">{formatRub(now)}</span>
        {a.updated !== '' && (
          <span
            className={`label text-[8px] ${stale || a.needs_confirmation ? 'opacity-90' : 'opacity-40'}`}
            data-testid={`confirmed-${a.bank}`}
            title={
              `Подтверждён ${formatRub(toKopecks(a.balance))} ${shortDate(a.updated)}` +
              (spent > 0 ? `, после этого списано ${formatRub(spent)}` : '')
            }
          >
            {(stale || a.needs_confirmation) && <span data-testid={`stale-${a.bank}`}>⚠ </span>}
            {shortDate(a.updated)}
            {spent > 0 && <span className="privacy-mask"> −{formatRub(spent)}</span>}
          </span>
        )}
      </span>
    </li>
  )
}
