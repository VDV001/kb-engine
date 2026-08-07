package domain

import (
	"testing"
	"time"
)

// Дата подтверждения — ДЕНЬ, а часы — МОМЕНТ, и сравнивать их напрямую нельзя.
//
// Лист «Счета» хранит день, excelize отдаёт его полуночью UTC. Команда пишет
// туда сегодняшнее число по местному времени. Между полуночью и пятью утра по
// книге (UTC+5) эти два представления расходятся: день уже наступил у
// владельца, но ещё не наступил в UTC, и полночь 8-го «позже», чем 19:19
// 7-го. Домен из-за этого отвергал счёт, который сам же и записал, — книга
// переставала читаться целиком: ни баланса, ни синка, ни вкладки финансов.
//
// Зона и час здесь прибиты намеренно. Тест, зависящий от настоящих часов,
// зелен девятнадцать часов в сутки и ничего не проверяет — а CI живёт в UTC,
// то есть не увидел бы этого никогда.
func TestNewAccount_updatedToday_acceptedNearMidnightInEasternZone(t *testing.T) {
	book := time.FixedZone("+05", 5*3600)
	// 2026-08-07 19:19 UTC = 2026-08-08 00:19 по книге: у владельца уже 8-е.
	now := func() time.Time { return time.Date(2026, 8, 7, 19, 19, 0, 0, time.UTC).In(book) }

	cases := []struct {
		name    string
		updated time.Time
		wantErr bool
	}{
		{
			name:    "сегодняшний день книги, пришедший из ячейки полуночью UTC",
			updated: time.Date(2026, 8, 8, 0, 0, 0, 0, time.UTC),
			wantErr: false,
		},
		{
			name:    "вчерашний день",
			updated: time.Date(2026, 8, 7, 0, 0, 0, 0, time.UTC),
			wantErr: false,
		},
		{
			name:    "завтрашний день остаётся будущим",
			updated: time.Date(2026, 8, 9, 0, 0, 0, 0, time.UTC),
			wantErr: true,
		},
		{
			name:    "пустая дата — счёт без подтверждения",
			updated: time.Time{},
			wantErr: false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := NewAccount("Сбербанк", Money{}, c.updated, now)
			if c.wantErr && err == nil {
				t.Fatalf("ждали отказ для %s, счёт принят", c.updated.Format(time.DateOnly))
			}
			if !c.wantErr && err != nil {
				t.Fatalf("ждали, что счёт примут: %v", err)
			}
		})
	}
}
