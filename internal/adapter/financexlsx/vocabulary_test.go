package financexlsx_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/financexlsx"
	"github.com/daniil/kb-engine/internal/domain"
)

// The workbook tells an account from a source by looking the value up on the
// Счета sheet — that sheet is the vocabulary, and a name missing from it has no
// meaning the reader can recover. Writing such a name anyway is a silent loss,
// and the property test missed it because its table only ever used Т-Банк,
// which the sheet knows.
//
// Both forms below were verified to break write→read before this guard:
//
//	account only     "Озон Банк" comes back as the source
//	account + source the account is simply gone
//
// The domain is right to leave the set open — the sheet lists five and closing
// it would reject the sixth the day one is opened. It is this book that cannot
// store what it cannot name, so the refusal belongs here, at the boundary, and
// it says which sheet to add the name to.
func TestApplyRows_refusesAnAccountTheBookCannotName(t *testing.T) {
	forms := map[string]domain.Transaction{
		"account only":       txWith(t, "01A", "Озон Банк", ""),
		"account and source": txWith(t, "01A", "Озон Банк", "Чек"),
	}

	for name, tx := range forms {
		t.Run(name, func(t *testing.T) {
			path := paired(t)

			err := financexlsx.ApplyRows(path, []domain.Transaction{tx}, nil, writeClock)
			if !errors.Is(err, financexlsx.ErrUnknownAccount) {
				t.Fatalf("ApplyRows error = %v, want ErrUnknownAccount", err)
			}
			if got := err.Error(); !strings.Contains(got, "Озон Банк") || !strings.Contains(got, "Счета") {
				t.Errorf("error %q names neither the account nor the sheet to add it to", got)
			}
		})
	}
}

// The mirror image: a source spelled like an account. The reader treats any
// value in Источник that the Счета sheet knows as the account, so "Сбербанк"
// entered as a source comes back as an account with the source gone.
//
// Refused rather than rewritten, because the two readings mean different things
// and only the owner knows which was meant.
func TestApplyRows_refusesASourceThatNamesAnAccount(t *testing.T) {
	path := paired(t)

	err := financexlsx.ApplyRows(path,
		[]domain.Transaction{txWith(t, "01A", "", "Сбербанк")}, nil, writeClock)
	if !errors.Is(err, financexlsx.ErrSourceNamesAnAccount) {
		t.Fatalf("ApplyRows error = %v, want ErrSourceNamesAnAccount", err)
	}
	if got := err.Error(); !strings.Contains(got, "Сбербанк") {
		t.Errorf("error %q does not name the value at issue", got)
	}
}

// A known account still writes, on every form the sheet can express. Without
// this the guard above could be satisfied by refusing everything.
func TestApplyRows_stillWritesAnAccountTheBookKnows(t *testing.T) {
	forms := map[string]domain.Transaction{
		"account only":       txWith(t, "01A", "Т-Банк", ""),
		"account and source": txWith(t, "01A", "Т-Банк", "Чек"),
		"source only":        txWith(t, "01A", "", "Чек"),
		"neither":            txWith(t, "01A", "", ""),
	}

	for name, tx := range forms {
		t.Run(name, func(t *testing.T) {
			path := paired(t)
			if err := financexlsx.ApplyRows(path, []domain.Transaction{tx}, nil, writeClock); err != nil {
				t.Fatalf("ApplyRows: %v", err)
			}
		})
	}
}
