import { describe, expect, it } from 'vitest'
import { ecg } from './health'

// Кардиограмма на фоне карточки — не украшение: её частота, амплитуда и
// скорость берутся из счёта здоровья, то есть график показывает то же число,
// что и цифра рядом. Логика вынесена из разметки, потому что проверить форму
// пути глазами в браузере нельзя, а regression здесь молчалив.
describe('ecg', () => {
  it('starts at the midline and returns to it', () => {
    const { d, width } = ecg(50)
    expect(d.startsWith('M0,50')).toBe(true)
    expect(d.endsWith(`L${width},50`)).toBe(true)
  })

  // Вторая половина — копия первой, сдвинутая на её длину: анимация гоняет
  // путь на -50%, и без точного повтора на стыке будет виден рывок.
  it('repeats itself so the scroll has no seam', () => {
    const { d, width } = ecg(70)
    const points = d.slice(1).split(' L').map((p) => p.split(',').map(Number))
    const half = width / 2
    const first = points.filter(([x]) => x < half)
    const second = points.filter(([x]) => x >= half && x < width)
    expect(second.length).toBe(first.length)
    second.forEach(([x, y], i) => {
      expect(x - half).toBeCloseTo(first[i][0], 6)
      expect(y).toBeCloseTo(first[i][1], 6)
    })
  })

  // Пустая база — почти ровная линия, полное здоровье — размашистые пики.
  it('grows the spikes with the score', () => {
    const spread = (score: number) => {
      const ys = ecg(score)
        .d.slice(1)
        .split(' L')
        .map((p) => Number(p.split(',')[1]))
      return Math.max(...ys) - Math.min(...ys)
    }
    expect(spread(0)).toBeLessThan(15)
    expect(spread(100)).toBeGreaterThan(70)
    expect(spread(100)).toBeGreaterThan(spread(45))
  })

  // Чем здоровее база, тем быстрее и заметнее пульс — но линия не должна
  // становиться ни невидимой, ни глухой заливкой.
  it('keeps speed and opacity within a sane band', () => {
    for (const score of [0, 45, 100]) {
      const { speed, opacity, strokeWidth } = ecg(score)
      expect(speed).toBeGreaterThanOrEqual(2.5)
      expect(speed).toBeLessThanOrEqual(8)
      expect(opacity).toBeGreaterThanOrEqual(0.2)
      expect(opacity).toBeLessThanOrEqual(0.7)
      expect(strokeWidth).toBeGreaterThanOrEqual(1.5)
    }
    expect(ecg(100).speed).toBeLessThan(ecg(0).speed)
    expect(ecg(100).opacity).toBeGreaterThan(ecg(0).opacity)
  })

  // Счёт приходит из API; отрицательный или больше сотни означал бы ошибку
  // выше по течению, но рисовать вывернутую наизнанку линию всё равно нельзя.
  it('clamps a score outside 0..100', () => {
    expect(ecg(-20)).toEqual(ecg(0))
    expect(ecg(300)).toEqual(ecg(100))
  })
})
