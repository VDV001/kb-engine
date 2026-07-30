import { Icon } from './Icon'

/**
 * Переключатель «прятать суммы», живущий в шапке и только на финансах.
 *
 * Скрыто по умолчанию и сбрасывается в скрытое при каждом заходе на вид:
 * безопасное состояние — то, в котором оказываешься, а не то, которое надо
 * не забыть включить перед тем, как показать экран другому.
 */
export function PrivacyToggle({ masked, onChange }: { masked: boolean; onChange: (v: boolean) => void }) {
  return (
    <div className="flex h-9 items-center gap-2">
      {/* text-xl и высота 9, как у остальных иконок ряда: раньше здесь стоял
          text-sm, и глаз был заметно мельче бургера рядом. */}
      <Icon
        name={masked ? 'visibility_off' : 'visibility'}
        className="text-xl text-on-surface-variant"
      />
      <label className="toggle-switch">
        <input
          type="checkbox"
          checked={masked}
          onChange={(e) => onChange(e.target.checked)}
          aria-label={masked ? 'Показать суммы' : 'Скрыть суммы'}
        />
        <span className="toggle-slider" />
      </label>
      <span className="label text-[10px] text-on-surface-variant">{masked ? 'Hidden' : 'Open'}</span>
    </div>
  )
}
