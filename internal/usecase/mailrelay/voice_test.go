package mailrelay_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/usecase/mailrelay"
)

// id1 and id2 are file ids as the server gives them.
const (
	id1 = "0123456789abcdef0123456789abcde1"
	id2 = "0123456789abcdef0123456789abcde2"
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

func voice(mode, id string, sealed []byte) mailrelay.Payload {
	sum := sha256.Sum256(sealed)
	return mailrelay.Payload{Kind: mailrelay.KindVoice, Text: mode, File: &mailrelay.FileRef{ID: id, SHA256: hex.EncodeToString(sum[:]), Size: len(sealed)}}
}

// Голос телефона (#648): диктовка — текст телефону; вопрос голосом — ход
// модели с расслышанным; запись удаляется, когда окно ее забрало.
func TestRelayHearsThePhone(t *testing.T) {
	files := &voiceFiles{m: map[string][]byte{id1: []byte("sealed-wav"), id2: []byte("sealed-wav")}}
	h := &hearer{}
	b, a := &box{}, &asker{}
	b.phone("c-1", "A", 1, voice(mailrelay.VoiceDictate, id1, []byte("sealed-wav")))
	b.phone("c-1", "A", 2, voice(mailrelay.VoiceAsk, id2, []byte("sealed-wav")))
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
	files := &voiceFiles{m: map[string][]byte{id1: []byte("other-wav")}}
	h := &hearer{}
	b := &box{}
	b.phone("c-1", "A", 1, voice(mailrelay.VoiceDictate, id1, []byte("sealed-wav")))
	b.phone("c-1", "A", 2, voice("sing", id1, []byte("sealed-wav")))
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
	b2.phone("c-1", "A", 1, voice(mailrelay.VoiceDictate, id1, []byte("sealed-wav")))
	if err := newRelay(b2, &asker{}, &store{}).Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := payloads(t, b2); len(got) != 1 || got[0].Kind != mailrelay.KindError || got[0].Text != mailrelay.DeskUnsupported {
		t.Fatalf("no voice: %+v", got)
	}
}

// --- ревью #660

// slowHearer blocks until its context ends; err — иначе текст с путем компьютера.
type slowHearer struct{ err error }

func (h slowHearer) Hear(ctx context.Context, _ []byte) (string, error) {
	if h.err != nil {
		return "", h.err
	}
	<-ctx.Done()
	return "", ctx.Err()
}

// checkingAsker records whether the recording was already gone when the model ran.
type checkingAsker struct {
	asker
	files     *voiceFiles
	goneAtAsk bool
}

func (a *checkingAsker) Ask(ctx context.Context, chat, q string) (string, string, error) {
	_, err := a.files.Get(ctx, id1)
	a.goneAtAsk = err != nil
	return a.asker.Ask(ctx, chat, q)
}

// Тихий вопрос голосом — «не расслышал», без push; запись удаляется сразу
// после сверки, до хода модели; при несовпадении — не удаляется (это могла
// быть ждущая запись другого телефона); плохой id и размер — отказ кодом без
// похода в хранилище; распознавание — со сроком и кодом, без причины.
func TestRelayVoiceReview(t *testing.T) {
	t.Run("silent question", func(t *testing.T) {
		files := &voiceFiles{m: map[string][]byte{id1: []byte("silence")}}
		b := &box{}
		b.phone("c-1", "A", 1, voice(mailrelay.VoiceAsk, id1, []byte("silence")))
		n := &notifier{b: b}
		r := newRelay(b, &asker{}, &store{})
		r.Files, r.Hear, r.Notifier = files, hearerFunc(func(context.Context, []byte) (string, error) { return " ", nil }), n
		if err := r.Tick(t.Context()); err != nil {
			t.Fatal(err)
		}
		if got := payloads(t, b); len(got) != 1 || got[0].Kind != mailrelay.KindError || got[0].Text != mailrelay.VoiceSilent || len(n.calls) != 0 {
			t.Fatalf("silent: %+v, notify %v — тишина вопроса — свой код, не «heard» диктовки", got, n.calls)
		}
	})
	t.Run("model failure after hearing still pushes", func(t *testing.T) {
		files := &voiceFiles{m: map[string][]byte{id1: []byte("sealed-wav")}}
		b := &box{}
		b.phone("c-1", "A", 1, voice(mailrelay.VoiceAsk, id1, []byte("sealed-wav")))
		n := &notifier{b: b}
		r := newRelay(b, &asker{fail: errors.New("нет ключа модели")}, &store{})
		r.Files, r.Hear, r.Notifier = files, &hearer{}, n
		_ = r.Tick(t.Context())
		if got := payloads(t, b); len(got) != 1 || got[0].Kind != mailrelay.KindError || got[0].Heard != "срок поставки" || len(n.calls) != 1 {
			t.Fatalf("model failure: %+v, notify %v — модель работала, ответ (отказ) ждут", got, n.calls)
		}
	})
	t.Run("deleted before the model turn", func(t *testing.T) {
		files := &voiceFiles{m: map[string][]byte{id1: []byte("sealed-wav")}}
		b := &box{}
		b.phone("c-1", "A", 1, voice(mailrelay.VoiceAsk, id1, []byte("sealed-wav")))
		a := &checkingAsker{files: files}
		r := mailrelay.Relay{Box: b, Crypto: crypto{}, Asker: a, Store: &store{}, Files: files, Hear: &hearer{}}
		if err := r.Tick(t.Context()); err != nil {
			t.Fatal(err)
		}
		if !a.goneAtAsk {
			t.Error("the recording waited in the bucket for the whole model turn")
		}
	})
	t.Run("mismatch keeps the piece", func(t *testing.T) {
		files := &voiceFiles{m: map[string][]byte{id1: []byte("other")}}
		b := &box{}
		b.phone("c-1", "A", 1, voice(mailrelay.VoiceDictate, id1, []byte("sealed-wav")))
		r := newRelay(b, &asker{}, &store{})
		r.Files, r.Hear = files, &hearer{}
		_ = r.Tick(t.Context())
		if len(files.deleted) != 0 {
			t.Errorf("deleted %v on a mismatch", files.deleted)
		}
	})
	t.Run("bad id and size", func(t *testing.T) {
		files := &voiceFiles{m: map[string][]byte{id1: []byte("sealed-wav")}}
		b := &box{}
		bad := voice(mailrelay.VoiceDictate, "../log", []byte("sealed-wav"))
		b.phone("c-1", "A", 1, bad)
		size := voice(mailrelay.VoiceDictate, id1, []byte("sealed-wav"))
		size.File.Size = 3
		b.phone("c-1", "A", 2, size)
		r := newRelay(b, &asker{}, &store{})
		r.Files, r.Hear = files, &hearer{}
		_ = r.Tick(t.Context())
		got := payloads(t, b)
		if len(got) != 2 || got[0].Text != mailrelay.VoiceGone || got[1].Text != mailrelay.VoiceMismatch {
			t.Fatalf("bad id / size: %+v", got)
		}
	})
	t.Run("hearing has a deadline and a code", func(t *testing.T) {
		for _, h := range []mailrelay.Hearer{slowHearer{}, slowHearer{err: errors.New("open /var/folders/x/kb-phone-1.wav: denied")}} {
			files := &voiceFiles{m: map[string][]byte{id1: []byte("sealed-wav")}}
			b := &box{}
			b.phone("c-1", "A", 1, voice(mailrelay.VoiceDictate, id1, []byte("sealed-wav")))
			r := newRelay(b, &asker{}, &store{})
			r.Files, r.Hear, r.HearTimeout = files, h, 50*time.Millisecond
			start := time.Now()
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second) // без срока в коде RED не висит
			_ = r.Tick(ctx)
			cancel()
			got := payloads(t, b)
			if time.Since(start) > 5*time.Second || len(got) != 1 || got[0].Text != mailrelay.VoiceUnheard || strings.Contains(got[0].Text, "/var") {
				t.Fatalf("hearing: %+v after %v", got, time.Since(start))
			}
		}
	})
}

type hearerFunc func(context.Context, []byte) (string, error)

func (f hearerFunc) Hear(ctx context.Context, b []byte) (string, error) { return f(ctx, b) }
