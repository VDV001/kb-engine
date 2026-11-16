package mail_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/mail"
)

type failingFiles struct{ err error }

func (f failingFiles) Put(context.Context, domain.UserID, string, []byte) error { return f.err }
func (f failingFiles) Get(context.Context, domain.UserID, string) ([]byte, error) {
	return nil, f.err
}
func (f failingFiles) Delete(context.Context, domain.UserID, string) error { return f.err }

// Запись не легла — id нет: вызывающий не сошлется на несуществующий кусок
// (ревью #659). Без хранилища файлов — свой отказ, а не общий ErrUnsupported.
func TestPutFileFailures(t *testing.T) {
	id, _ := domain.NewUserID("alpha")
	boom := errors.New("bucket down")
	s := mail.Service{Files: failingFiles{boom}, Now: time.Now}
	if file, err := s.PutFile(t.Context(), id, domain.MailPhone, []byte("x")); file != "" || !errors.Is(err, boom) {
		t.Errorf("failed put: id %q, %v", file, err)
	}
	off := mail.Service{Now: time.Now}
	if _, err := off.PutFile(t.Context(), id, domain.MailPhone, []byte("x")); !errors.Is(err, domain.ErrMailFilesOff) {
		t.Errorf("no files: %v", err)
	}
	if _, err := off.GetFile(t.Context(), id, "0123456789abcdef0123456789abcdef"); !errors.Is(err, domain.ErrMailFilesOff) {
		t.Errorf("no files, get: %v", err)
	}
}
