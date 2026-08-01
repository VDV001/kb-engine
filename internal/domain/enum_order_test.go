package domain_test

import (
	"testing"

	"github.com/daniil/kb-engine/internal/domain"
)

// Список для показа и множество для проверки обязаны быть одним и тем же
// знанием. Разъедутся — экран предложит значение, которое домен отвергнет,
// и наоборот: новое значение не появится в списке выбора.
func TestLifecycles_matchWhatIsAccepted(t *testing.T) {
	got := domain.Lifecycles()
	if len(got) == 0 {
		t.Fatal("список состояний пуст")
	}
	for _, v := range got {
		if _, err := domain.NewLifecycle(v); err != nil {
			t.Errorf("список предлагает %q, домен его не принимает: %v", v, err)
		}
	}
	if got[0] != "active" {
		t.Errorf("первым идёт %q — список должен начинаться с состояния живой записи", got[0])
	}
}

func TestVerdicts_matchWhatIsAccepted(t *testing.T) {
	got := domain.Verdicts()
	if len(got) == 0 {
		t.Fatal("список вердиктов пуст")
	}
	for _, v := range got {
		if _, err := domain.NewVerdict(v); err != nil {
			t.Errorf("список предлагает %q, домен его не принимает: %v", v, err)
		}
	}
}

// Обратная сторона: ни одно принимаемое значение не должно остаться вне списка.
func TestEnums_haveNoValueOutsideTheList(t *testing.T) {
	for _, tc := range []struct {
		name    string
		list    []string
		extra   string
		accepts func(string) error
	}{
		{"lifecycle", domain.Lifecycles(), "dead-end", func(s string) error { _, err := domain.NewLifecycle(s); return err }},
		{"verdict", domain.Verdicts(), "skip-unavailable", func(s string) error { _, err := domain.NewVerdict(s); return err }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.accepts(tc.extra); err != nil {
				t.Fatalf("%q не принимается доменом: %v", tc.extra, err)
			}
			found := false
			for _, v := range tc.list {
				if v == tc.extra {
					found = true
				}
			}
			if !found {
				t.Errorf("%q принимается доменом, но отсутствует в списке", tc.extra)
			}
		})
	}
}
