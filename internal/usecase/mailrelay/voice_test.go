package mailrelay_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"

	"github.com/daniil/kb-engine/internal/usecase/mailrelay"
)

// voiceFiles is the mailbox files with the phone's recordings.
type voiceFiles struct {
	m       map[string][]byte
	deleted []string
}

func (f *voiceFiles) Get(_ context.Context, id string) ([]byte, error) {
	b, ok := f.m[id]
	if !ok {
		return nil, errors.New("no such file")
	}
	return b, nil
}

func (f *voiceFiles) Delete(_ context.Context, id string) error {
	f.deleted = append(f.deleted, id)
	delete(f.m, id)
	return nil
}

// hearer «распознает» запечатанную запись; calls — сколько раз звали.
type hearer struct{ calls int }

func (h *hearer) Hear(_ context.Context, sealed []byte) (string, error) {
	h.calls++
	if string(sealed) == "sealed-wav" {
		return "срок поставки", nil
	}
	return "", errors.New("not a recording")
}

func voice(mode string, id string, sealed []byte) mailrelay.Payload {
	sum := sha256.Sum256(sealed)
	return mailrelay.Payload{Kind: mailrelay.KindVoice, Text: mode, File: &mailrelay.FileRef{ID: id, SHA256: hex.EncodeToString(sum[:]), Size: len(sealed)}}
}

// Голос телефона (#648): диктовка — текст телефону; вопрос голосом — ход
// модели с расслышанным; запись удаляется, когда окно ее забрало.
func TestRelayHearsThePhone(t *testing.T) {
	files := &voiceFiles{m: map[string][]byte{"f1": []byte("sealed-wav"), "f2": []byte("sealed-wav")}}
	h := &hearer{}
	b, a := &box{}, &asker{}
	b.phone("c-1", "A", 1, voice(mailrelay.VoiceDictate, "f1", []byte("sealed-wav")))
	b.phone("c-1", "A", 2, voice(mailrelay.VoiceAsk, "f2", []byte("sealed-wav")))
	r := newRelay(b, a, &store{})
	r.Files, r.Hear = files, h
	if err := r.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	got := payloads(t, b)
	if len(got) != 2 || got[0].Kind != mailrelay.KindHeard || got[0].Text != "срок поставки" {
		t.Fatalf("dictation answer %+v", got)
	}
	if got[1].Kind != mailrelay.KindAnswer || got[1].Heard != "срок поставки" || got[1].Text != "ответ на срок поставки" || len(a.asked) != 1 {
		t.Errorf("voice question answer %+v, asked %v", got[1], a.asked)
	}
	if len(files.deleted) != 2 {
		t.Errorf("deleted %v — the recordings stay in the bucket", files.deleted)
	}
}

// Запись не та, что назвало сообщение (сервер подменил кусок) — отказ кодом,
// распознавание не зовется; окно без голоса — отказ кодом.
func TestRelayRefusesAForeignRecording(t *testing.T) {
	files := &voiceFiles{m: map[string][]byte{"f1": []byte("other-wav")}}
	h := &hearer{}
	b := &box{}
	b.phone("c-1", "A", 1, voice(mailrelay.VoiceDictate, "f1", []byte("sealed-wav")))
	b.phone("c-1", "A", 2, voice("sing", "f1", []byte("sealed-wav")))
	r := newRelay(b, &asker{}, &store{})
	r.Files, r.Hear = files, h
	if err := r.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	got := payloads(t, b)
	if len(got) != 1 || got[0].Kind != mailrelay.KindError || got[0].Text != mailrelay.VoiceMismatch || h.calls != 0 {
		t.Fatalf("foreign recording: %+v, heard %d times — an unknown mode is not a request", got, h.calls)
	}
	b2 := &box{}
	b2.phone("c-1", "A", 1, voice(mailrelay.VoiceDictate, "f1", []byte("sealed-wav")))
	if err := newRelay(b2, &asker{}, &store{}).Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := payloads(t, b2); len(got) != 1 || got[0].Kind != mailrelay.KindError || got[0].Text != mailrelay.DeskUnsupported {
		t.Fatalf("no voice: %+v", got)
	}
}
