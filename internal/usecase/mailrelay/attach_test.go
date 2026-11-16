package mailrelay_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/daniil/kb-engine/internal/usecase/mailrelay"
)

// opener «расшифровывает» кусок: снимает префикс sealed:.
type opener struct{}

func (opener) OpenFile(sealed []byte) ([]byte, error) {
	return bytes.TrimPrefix(sealed, []byte("sealed:")), nil
}

// attachAsker records the files of a turn.
type attachAsker struct {
	asker
	files []mailrelay.File
}

func (a *attachAsker) AskAttached(ctx context.Context, chat, q string, files []mailrelay.File, _ func(mailrelay.Step)) (mailrelay.Answer, error) {
	a.files = files
	c, text, err := a.Ask(ctx, chat, q)
	return mailrelay.Answer{Chat: c, Text: text}, err
}

func piece(id string, sealed []byte) mailrelay.FileRef {
	sum := sha256.Sum256(sealed)
	return mailrelay.FileRef{ID: id, SHA256: hex.EncodeToString(sum[:]), Size: len(sealed)}
}

func attachQuestion(size int, pieces ...mailrelay.FileRef) mailrelay.Payload {
	return mailrelay.Payload{Kind: mailrelay.KindQuestion, Text: "что в договоре?",
		Attach: &mailrelay.Attachment{Name: "договор.pdf", Type: "application/pdf", Size: size, Pieces: pieces}}
}

// Файл к вопросу (#650): куски — по порядку, каждый сверен, длина — тоже;
// ход модели получает файл целиком, куски удаляются. Не совпало — отказ кодом,
// куски остаются; больше 10 МБ — свой код; окно без вложений — отказ.
func TestRelayAttachesThePhoneFile(t *testing.T) {
	p1, p2 := []byte("sealed:%PDF-1"), []byte("sealed:.7 end")
	t.Run("assembled", func(t *testing.T) {
		files := &voiceFiles{m: map[string][]byte{id1: p1, id2: p2}}
		b, a := &box{}, &attachAsker{}
		b.phone("c-1", "A", 1, attachQuestion(len("%PDF-1.7 end"), piece(id1, p1), piece(id2, p2)))
		r := newRelay(b, &asker{}, &store{})
		r.Asker, r.Files, r.Open = a, files, opener{}
		if err := r.Tick(t.Context()); err != nil {
			t.Fatal(err)
		}
		if len(a.files) != 1 || string(a.files[0].Data) != "%PDF-1.7 end" || a.files[0].Name != "договор.pdf" || a.files[0].Type != "application/pdf" {
			t.Fatalf("turn files %+v", a.files)
		}
		if got := payloads(t, b); len(got) != 1 || got[0].Kind != mailrelay.KindAnswer {
			t.Errorf("answer %+v", got)
		}
		if len(files.deleted) != 2 {
			t.Errorf("deleted %v", files.deleted)
		}
	})
	for name, c := range map[string]struct {
		stored map[string][]byte
		q      mailrelay.Payload
		code   string
	}{
		"swapped by the server": {map[string][]byte{id1: p2, id2: p1}, attachQuestion(12, piece(id1, p1), piece(id2, p2)), mailrelay.VoiceMismatch},
		"cut":                   {map[string][]byte{id1: p1, id2: p2}, attachQuestion(12, piece(id1, p1)), mailrelay.VoiceMismatch},
		"too large":             {map[string][]byte{id1: p1, id2: p2}, attachQuestion(mailrelay.MaxAttachment+1, piece(id1, p1)), mailrelay.AttachTooLarge},
		"bad id":                {map[string][]byte{id1: p1, id2: p2}, attachQuestion(12, piece("../log", p1)), mailrelay.VoiceGone},
	} {
		t.Run(name, func(t *testing.T) {
			files := &voiceFiles{m: c.stored}
			b, a := &box{}, &attachAsker{}
			b.phone("c-1", "A", 1, c.q)
			r := newRelay(b, &asker{}, &store{})
			r.Asker, r.Files, r.Open = a, files, opener{}
			_ = r.Tick(t.Context())
			got := payloads(t, b)
			if len(got) != 1 || got[0].Kind != mailrelay.KindError || got[0].Text != c.code || a.files != nil || len(files.deleted) != 0 {
				t.Fatalf("%+v, turn files %v, deleted %v", got, a.files, files.deleted)
			}
		})
	}
	t.Run("window without attachments", func(t *testing.T) {
		b := &box{}
		b.phone("c-1", "A", 1, attachQuestion(12, piece(id1, p1)))
		_ = newRelay(b, &asker{}, &store{}).Tick(t.Context())
		if got := payloads(t, b); len(got) != 1 || got[0].Text != mailrelay.DeskUnsupported {
			t.Fatalf("%+v", got)
		}
	})
}
