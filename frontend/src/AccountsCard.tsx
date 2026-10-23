import { useState } from 'react'
import type { Account, AccountGroup } from './api'
import { formatMoney, formatRub, toKopecks, unitOf } from './money'

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
// Кузнецов». Она значит «одно из нескольких одного рода», и витрина читает её,
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
  groups: engineGroups,
  free,
  expenses,
  income,
  today,
  transfersExcluded = 0,
}: {
  accounts: Account[]
  /**
   * Итоги родов от движка. Складывая их сама, витрина заводит вторую копию
   * правила про деньги — и та копия не знает о валюте: сложит доллары с
   * рублями и назовёт результат рублями (#332).
   *
   * Поля может не быть у старой сборки сервера. Тогда карточка считает сама,
   * как считала раньше: на рублёвой книге это верно, а промолчать про рода
   * вовсе — хуже.
   */
  groups?: AccountGroup[]
  /**
   * Свободные деньги, посчитанные движком. Витрина их не выводит сама: рода
   * счетов ведут себя по-разному (отложенное и одолженное не свободно,
   * обязательство ВЫЧИТАЕТСЯ, потому что лежит на обычных счетах и уже
   * посчитано), и это правило про деньги, а не про вёрстку.
   */
  free?: string
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

  // В общий итог идёт ОЦЕНКА в валюте книги, а не номинал: 500 долларов —
  // это не 500 рублей. Счёт, оценить который нечем, не входит вовсе — иначе
  // его номинал молча притворился бы рублями.
  const valued = (a: Account) => {
    if (a.unvalued) return null
    if (a.base_value !== undefined) return toKopecks(a.base_value)
    return now(a)
  }
  const total = accounts.reduce((n, a) => n + (valued(a) ?? 0), 0)
  const engineGroup = (name: string) => engineGroups?.find((g) => g.group === name)

  // Порядок групп — тот, в котором счета стоят в книге: владелец сам решил, что
  // за чем идёт на листе, и витрине незачем это переставлять.
  // Счета по нулю прячутся за одной строкой: закрытые долги и пустые заначки
  // занимали больше места, чем деньги, и полосу приходилось читать поверх них.
  const [showZero, setShowZero] = useState(false)
  // Ноль, который просит сверки, не прячется: его ⚠ и есть причина смотреть.
  const folded = (a: Account) => now(a) === 0 && !a.needs_confirmation
  const zeroCount = accounts.filter(folded).length
  const visible = showZero ? accounts : accounts.filter((a) => !folded(a))
  const foreign = foreignTotals(accounts)

  const groups: { group: string; accounts: Account[] }[] = []
  for (const a of visible) {
    const { group } = splitAccountName(a)
    const found = groups.find((g) => g.group === group)
    if (found) found.accounts.push(a)
    else groups.push({ group, accounts: [a] })
  }
  const plain = groups.find((g) => g.group === '')?.accounts ?? []
  const kinds = groups.filter((g) => g.group !== '')

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
          {foreign.length > 0 && (
            <span className="label text-[8px] opacity-60" data-testid="accounts-foreign">
              + <span className="privacy-mask font-bold">{foreign.join(' · ')}</span>
            </span>
          )}
          {kinds.length > 0 && free !== undefined && (
            <span className="label text-[8px] opacity-60" data-testid="accounts-free">
              свободно <span className="privacy-mask font-bold">{formatRub(toKopecks(free))}</span>
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
          {plain.length > 0 && (
            <div className="mb-2 flex items-baseline justify-between gap-3">
              <span className="label text-[8px] tracking-[0.2em] opacity-50" data-testid="group-Банки">
                Банки
              </span>
              <span className="privacy-mask text-xs font-bold opacity-80" data-testid="group-total-Банки">
                {formatRub(plain.reduce((n, a) => n + (valued(a) ?? 0), 0))}
              </span>
            </div>
          )}
          <ul className="space-y-2.5">
            {byBank(plain).map(({ bank, accounts: same }) =>
              same.length === 1 ? (
                <AccountRow key={same[0].bank} account={same[0]} label={bank} today={today} />
              ) : (
                <MultiCurrencyRow key={bank} bank={bank} accounts={same} today={today} />
              ),
            )}
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
                  {formatRub(
                    toKopecks(
                      engineGroup(k.group)?.total ??
                        String(k.accounts.reduce((n, a) => n + (valued(a) ?? 0), 0) / 100),
                    ),
                  )}
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
              <GroupNotes group={engineGroup(k.group)} shown={k.accounts} />
            </div>
          ))}
          {zeroCount > 0 && (
            <button
              type="button"
              className="label mt-3 text-[9px] opacity-60 hover:opacity-90"
              data-testid="accounts-zero-toggle"
              onClick={() => setShowZero((v) => !v)}
            >
              {showZero ? '‹ скрыть счета по нулю' : `› ещё ${zeroCount} ${accountsWord(zeroCount)} по нулю`}
            </button>
          )}
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
 * Подписи под родом: по какому курсу сложен итог и кто в него НЕ вошёл.
 *
 * Курс без дня выглядит текущей оценкой, будучи ценой входа, — движок за
 * курсом никуда не ходит. А итог, умолчавший о неоценённых счетах, утверждает
 * больше, чем знает: сумма выглядит полной, будучи частичной.
 */
function GroupNotes({ group, shown }: { group?: AccountGroup; shown: Account[] }) {
  if (!group) return null
  // Подписи только о том, что на экране: курс свёрнутого нулевого счёта
  // читался бы как курс всего рода.
  const codes = new Set(shown.map((a) => a.currency ?? ''))
  const names = new Set(shown.map((a) => a.bank))
  const rates = (group.rates ?? []).filter((r) => codes.has(r.currency))
  const unvalued = (group.unvalued ?? []).filter((n) => names.has(n))
  if (rates.length === 0 && unvalued.length === 0) return null

  return (
    <div className="mt-1.5 space-y-0.5">
      {rates.length > 0 && (
        <p className="label text-[8px] opacity-50" data-testid={`group-rates-${group.group}`}>
          {rates
            .map((r) => `${unitOf(r.currency)} по ${formatRub(toKopecks(r.per_unit))} на ${shortDate(r.on)}`)
            .join(' · ')}
        </p>
      )}
      {unvalued.length > 0 && (
        <p className="label text-[8px] opacity-60" data-testid={`group-unvalued-${group.group}`}>
          не в итоге: {unvalued.map((n) => splitName(n)).join(', ')} — курс неизвестен
        </p>
      )}
    </div>
  )
}

/** Имя счёта без рода: «Кубышка → Евро» → «Евро». */
function splitName(bank: string): string {
  const at = bank.indexOf(GROUP_SEPARATOR)
  return at < 0 ? bank : bank.slice(at + GROUP_SEPARATOR.length).trim()
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
        <span className="privacy-mask text-xs font-bold" data-testid={`amount-${a.bank}`}>
          {formatMoney(now, a.currency ?? '')}
        </span>
        {/* Оценка идёт ВТОРОЙ строкой, мельче: главное число у валютного
            счёта — сколько там валюты, а не во что её оценили по курсу
            месячной давности. */}
        {a.base_value !== undefined && !a.unvalued && (
          <span className="label text-[8px] opacity-40" data-testid={`valued-${a.bank}`}>
            ≈ <span className="privacy-mask">{formatRub(toKopecks(a.base_value))}</span>
          </span>
        )}
        {a.unvalued && (
          <span className="label text-[8px] opacity-60" data-testid={`unvalued-${a.bank}`}>
            курс неизвестен
          </span>
        )}
        {a.updated !== '' && (
          <span
            className={`label text-[8px] ${stale || a.needs_confirmation ? 'opacity-90' : 'opacity-40'}`}
            data-testid={`confirmed-${a.bank}`}
            title={
              `Подтверждён ${formatMoney(toKopecks(a.balance), a.currency ?? '')} ${shortDate(a.updated)}` +
              (spent > 0 ? `, после этого списано ${formatMoney(spent, a.currency ?? '')}` : '')
            }
          >
            {(stale || a.needs_confirmation) && <span data-testid={`stale-${a.bank}`}>⚠ </span>}
            {shortDate(a.updated)}
            {spent > 0 && <span className="privacy-mask"> −{formatMoney(spent, a.currency ?? '')}</span>}
          </span>
        )}
      </span>
    </li>
  )
}

/** «1 счёт», «2 счёта», «5 счетов». */
function accountsWord(n: number): string {
  const d = n % 10
  const dd = n % 100
  if (d === 1 && dd !== 11) return 'счёт'
  if (d >= 2 && d <= 4 && (dd < 12 || dd > 14)) return 'счёта'
  return 'счетов'
}

/**
 * Обычные счета, собранные по банку: «Банк USD» и «Банк UZS» — один банк в двух
 * валютах. Банк узнаётся по имени, в конце которого стоит код валюты самого
 * счёта; совпадение с чужим кодом ничего не склеивает.
 */
function byBank(plain: Account[]): { bank: string; accounts: Account[] }[] {
  const out: { bank: string; accounts: Account[] }[] = []
  for (const a of plain) {
    const code = a.currency ?? ''
    const suffix = ` ${code}`
    const bank =
      code !== '' && a.bank.toUpperCase().endsWith(suffix.toUpperCase())
        ? a.bank.slice(0, -suffix.length).trim()
        : a.bank
    const found = out.find((b) => b.bank === bank)
    if (found) found.accounts.push(a)
    else out.push({ bank, accounts: [a] })
  }
  return out
}

/**
 * Валюта, которую нечем оценить, суммой по каждой единице: «24,50 $», «75,00 сум».
 * В рублёвый итог она не входит, и без этой строки её на полосе не было бы вовсе.
 */
function foreignTotals(accounts: Account[]): string[] {
  const sums = new Map<string, number>()
  for (const a of accounts) {
    if (!a.unvalued) continue
    const minor = toKopecks(a.current ?? a.balance)
    if (minor === 0) continue
    const code = a.currency ?? ''
    sums.set(code, (sums.get(code) ?? 0) + minor)
  }
  return [...sums].map(([code, minor]) => formatMoney(minor, code))
}

/**
 * Один банк в нескольких валютах: имя один раз, суммы столбиком. Сигналы строки
 * счёта не теряются при склейке: оценка в рублях у каждой суммы, «курс
 * неизвестен» и ⚠, если хоть одна из сумм устарела или просит сверки.
 */
function MultiCurrencyRow({ bank, accounts, today }: { bank: string; accounts: Account[]; today: string }) {
  // Самая ранняя дата: строка не должна выглядеть свежее самого старого числа в ней.
  const dates = accounts.map((a) => a.updated).filter((d) => d !== '').sort()
  const warn =
    accounts.some((a) => a.needs_confirmation) || (dates.length > 0 && daysBetween(dates[0], today) > STALE_AFTER_DAYS)
  return (
    <li className="flex items-start gap-2" data-testid={`bank-${bank}`}>
      <span className="mt-1 size-2 shrink-0 rounded-full bg-current opacity-50" />
      <span className="label flex-1 text-[10px] opacity-80">{bank}</span>
      <span className="flex flex-col items-end">
        {accounts.map((a) => {
          const spent = a.spent ? toKopecks(a.spent) : 0
          return (
            <span key={a.bank} className="flex flex-col items-end">
              <span
                className="privacy-mask text-xs font-bold"
                data-testid={`amount-${a.bank}`}
                title={
                  `Подтверждён ${formatMoney(toKopecks(a.balance), a.currency ?? '')} ${shortDate(a.updated)}` +
                  (spent > 0 ? `, после этого списано ${formatMoney(spent, a.currency ?? '')}` : '')
                }
              >
                {formatMoney(toKopecks(a.current ?? a.balance), a.currency ?? '')}
              </span>
              {a.base_value !== undefined && !a.unvalued && (
                <span className="label text-[8px] opacity-40">
                  ≈ <span className="privacy-mask">{formatRub(toKopecks(a.base_value))}</span>
                </span>
              )}
              {a.unvalued && (
                <span className="label text-[8px] opacity-60" data-testid={`unvalued-${a.bank}`}>
                  курс неизвестен
                </span>
              )}
            </span>
          )
        })}
        {dates.length > 0 && (
          <span className={`label text-[8px] ${warn ? 'opacity-90' : 'opacity-40'}`} data-testid={`confirmed-${bank}`}>
            {warn && <span data-testid={`stale-${bank}`}>⚠ </span>}
            {shortDate(dates[0])}
          </span>
        )}
      </span>
    </li>
  )
}
