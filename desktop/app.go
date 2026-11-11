package main

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
	"github.com/daniil/kb-engine/internal/adapter/kbserver"
	"github.com/daniil/kb-engine/internal/usecase/portrait"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// fileChecker is the part of the file index the app needs: the same trust
// boundary the API uses, so a path the index would refuse is not opened
// either. Check, а не Read: ради разрешения файл целиком не читается.
//
// ponytail: между проверкой и open файл можно подменить ссылкой (open
// получает путь, а не дескриптор). Потолок: нужен доступ на запись в
// индексируемую папку. Путь: открывать через NSWorkspace по дескриптору.
type fileChecker interface {
	Check(path string) error
}

// App is bound into the window: its exported methods are callable from the
// page as window.go.main.App.<Method>.
type App struct {
	ctx      context.Context
	files    fileChecker
	settings *folderSettings
	listen   *dictation  // диктовка в поле чата (listen.go)
	voice    *speaker    // голос ответа в разговоре без рук (speak.go)
	google   *googleDesk // подключения Google (googleconn.go)
	index    *indexer
	chat     *chatDesk // nil — чата нет (тесты без него)
	access   *accessDesk
	memory   *memoryDesk
	browser  *browserDesk // кнопки вкладки «Браузер»
	run      func(name string, args ...string) error
}

func newApp(files fileChecker, settings *folderSettings, index *indexer) *App {
	return &App{files: files, settings: settings, index: index, run: func(name string, args ...string) error {
		return exec.Command(name, args...).Run()
	}}
}

// Sources returns the sources in priority order with their state and counts.
// Открытие экрана заодно возвращает в индекс папки, что снова доступны, не
// дожидаясь плановой пересборки.
func (a *App) Sources() sourcesView {
	a.settings.reconcile()
	return a.settings.GetSources()
}

// SetSources validates and saves the sources, then reindexes in the background.
func (a *App) SetSources(list []desktopconfig.Source, exclude []string, tokens map[string]string) error {
	return a.settings.SetSources(list, exclude, tokens)
}

// Modules returns the window modules with whether each is on.
func (a *App) Modules() []desktopconfig.Module { return a.settings.Modules() }

// SetModules saves which modules are on.
func (a *App) SetModules(list []desktopconfig.Module) error { return a.settings.SetModules(list) }

// SetModuleServer saves where the module takes its data (финансы: сервер или этот Mac).
func (a *App) SetModuleServer(id desktopconfig.ModuleID, server string) error {
	return a.settings.SetModuleServer(id, server)
}

// Tabs returns the search tabs after «Все».
func (a *App) Tabs() []desktopconfig.Tab { return a.settings.Tabs() }

// SetTabs saves the search tabs.
func (a *App) SetTabs(list []desktopconfig.Tab) error { return a.settings.SetTabs(list) }

// Accents returns the window's look: режим и свой набор человека.
func (a *App) Accents() desktopconfig.Accents { return a.settings.Accents() }

// SetAccents saves the window's look.
func (a *App) SetAccents(look desktopconfig.Accents) error { return a.settings.SetAccents(look) }

// NewSourceID returns an id for a source the person is adding: к id сервера
// привязан токен, и он нужен странице до сохранения списка.
func (a *App) NewSourceID(kind string) string { return desktopconfig.NewID(desktopconfig.Kind(kind)) }

// ServerStates returns the live state of the server sources; панель зовет его
// после того, как нарисовала список.
func (a *App) ServerStates() []sourceRow { return a.settings.ServerStates() }

// CheckServer is the «Проверить» button of the server form.
func (a *App) CheckServer(id, url, token string) (kbserver.Info, error) {
	return a.settings.CheckServer(id, url, token)
}

// Problems returns what the sources cannot do now, with the reason and what
// to do. fresh — опросить серверы, как ServerStates (≤ probeTimeout, Связка
// ключей): открытие панели и сохранение. Без fresh — последний известный
// ответ: строка статуса обновляется каждые 30 с и не ходит ни в сеть, ни в
// Связку (ревью #457).
func (a *App) Problems(fresh bool) problemsView {
	rows := a.settings.GetSources().Sources
	servers := a.settings.cachedServerStates()
	if fresh {
		servers = a.settings.ServerStates()
	}
	live := map[string]sourceRow{}
	for _, r := range servers {
		live[r.ID] = r
	}
	for i, r := range rows {
		if s, ok := live[r.ID]; ok {
			rows[i] = s
		}
	}
	st := a.index.Status()
	st.Unrecognized, st.UnrecognizedCount = a.index.unrecognizedRows()
	return collectProblems(rows, st, a.settings.home)
}

// ChatSettings returns the chat model settings, без ключа.
func (a *App) ChatSettings() chatSettingsView { return a.chat.Settings() }

// SetChatSettings saves the chat model; key — новый ключ, пусто — прежний.
func (a *App) SetChatSettings(c desktopconfig.Chat, key string) error {
	return a.chat.SetSettings(c, key)
}

// BrowserState returns the Browser tab's controls: установленные браузеры, выбор и работает ли сервер.
func (a *App) BrowserState() browserView { return a.browser.View() }

// BrowserChoose saves the browser for the next start; пусто — «Автоматически».
func (a *App) BrowserChoose(path string) error { return a.browser.Choose(path) }

// BrowserStart launches the browser server (kbengine browser serve) with the chosen browser.
func (a *App) BrowserStart() error { return a.browser.Start() }

// BrowserStop stops the browser server this window started.
func (a *App) BrowserStop() error { return a.browser.Stop() }

// AccessOpen asks Touch ID to open the access container (#494).
func (a *App) AccessOpen() error { return a.access.Open() }

// AccessClose locks the access container.
func (a *App) AccessClose() { a.access.Close() }

// AccessList returns the access cards with every word of q — без паролей.
func (a *App) AccessList(q string) ([]accessView, error) { return a.access.List(q) }

// AccessAdd stores an access card and its password.
func (a *App) AccessAdd(in accessIn, password string) error { return a.access.Add(in, password) }

// AccessReveal returns a password after Touch ID.
func (a *App) AccessReveal(id string) (string, error) { return a.access.Reveal(id) }

// AccessUpdate changes an access card (and its password, if given) after Touch ID.
func (a *App) AccessUpdate(id string, in accessIn, password string) error {
	return a.access.Update(id, in, password)
}

// AccessDelete removes an access after Touch ID.
func (a *App) AccessDelete(id string) error { return a.access.Delete(id) }

// AccessCopy puts a password on the clipboard after Touch ID.
func (a *App) AccessCopy(id string) error { return a.access.Copy(id) }

// MachineList returns the machines with every word of q — без паролей, только в открытом хранилище (#549).
func (a *App) MachineList(q string) ([]machineView, error) { return a.access.Machines(q) }

// MachineAdd stores a machine and, for a password sign-in, its password.
func (a *App) MachineAdd(in machineIn, password string) error {
	return a.access.AddMachine(in, password)
}

// MachineUpdate changes a machine (and its password, if given).
func (a *App) MachineUpdate(id string, in machineIn, password string) error {
	return a.access.UpdateMachine(id, in, password)
}

// MachineDelete removes a machine after Touch ID.
func (a *App) MachineDelete(id string) error { return a.access.DeleteMachine(id) }

// MachineReveal returns a machine password after Touch ID.
func (a *App) MachineReveal(id string) (string, error) { return a.access.RevealMachinePassword(id) }

// MachineCopy puts a machine password on the clipboard after Touch ID.
func (a *App) MachineCopy(id string) error { return a.access.CopyMachinePassword(id) }

// Stats returns the model-call statistics of the last days (экран «Статистика»).
func (a *App) Stats(days int) (statsView, error) { return a.chat.Stats(days) }

// StatsMoney returns the spend of the current month (экран «Деньги», #493).
func (a *App) StatsMoney() (moneyView, error) { return a.chat.StatsMoney() }

// SetModelPrice sets a model's own price per million tokens; пустые — цена шлюза.
func (a *App) SetModelPrice(model, in, out string) error { return a.chat.SetModelPrice(model, in, out) }

// SetSpendLimit sets the monthly spend limit in dollars; пусто — без лимита.
func (a *App) SetSpendLimit(usd string) error { return a.chat.SetSpendLimit(usd) }

// Portrait returns «Что DVK помнит обо мне» (#479).
func (a *App) Portrait() (portraitView, error) { return a.chat.Portrait() }

// PortraitAdd adds a fact typed by the person.
func (a *App) PortraitAdd(section, text string) error { return a.chat.PortraitAdd(section, text) }

// PortraitRestore puts a forgotten fact back as it was.
func (a *App) PortraitRestore(f factView) error { return a.chat.PortraitRestore(f) }

// PortraitEdit changes a fact text.
func (a *App) PortraitEdit(id, text string) error { return a.chat.PortraitEdit(id, text) }

// PortraitRemove forgets a fact.
func (a *App) PortraitRemove(id string) error { return a.chat.PortraitRemove(id) }

// PortraitApprove lets a fact into the chat; text — правка перед одобрением.
func (a *App) PortraitApprove(id, text string) error { return a.chat.PortraitApprove(id, text) }

// PortraitCompress asks the model for a shorter portrait.
func (a *App) PortraitCompress() (compressionView, error) { return a.chat.PortraitCompress() }

// PortraitApplyCompressed replaces the approved facts with the accepted proposal.
func (a *App) PortraitApplyCompressed(c compressionView) error {
	return a.chat.PortraitApplyCompressed(c)
}

// PickPortraitDir opens the folder chooser for the portrait; "" — отмена.
func (a *App) PickPortraitDir() (string, error) {
	return runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title:                "Папка портрета «Что DVK помнит обо мне»",
		CanCreateDirectories: true,
	})
}

// Chats lists the chats, newest first.
func (a *App) Chats() ([]chatSummary, error) { return a.chat.List() }

// Chat returns one chat with its messages and documents.
func (a *App) Chat(id string) (chatRecord, error) { return a.chat.Get(id) }

// Ask answers a question: чат, вопрос, приложенное и источники этого чата.
func (a *App) Ask(r askReq) (chatRecord, error) { return a.chat.Ask(r) }

// MemoryState returns the memory screen: слои, очередь, журнал, нарушения (ADR-0008).
func (a *App) MemoryState() (memoryView, error) { return a.memory.State() }

// MemoryInit creates the memory skeleton.
func (a *App) MemoryInit() error { return a.memory.Init() }

// MemoryApprove moves a queued note to "cold" or "hot".
func (a *App) MemoryApprove(name, to string) error { return a.memory.Approve(name, to) }

// MemoryReject archives a queued note to the journal.
func (a *App) MemoryReject(name string) error { return a.memory.Reject(name) }

// MemorySetHotLimit saves the ceiling of the hot layer in characters.
func (a *App) MemorySetHotLimit(n int) error { return a.memory.SetHotLimit(n) }

// MemorySeal accepts the current HOT.md after a change made outside the window and
// kbengine; по Touch ID.
func (a *App) MemorySeal() error { return a.memory.Seal() }

// MemoryReview records an inspection of the memory (по Touch ID).
func (a *App) MemoryReview() error { return a.memory.Review() }

// MemoryConfirmRule records that the person keeps a rule of the hot layer (по Touch ID).
func (a *App) MemoryConfirmRule(text string) error { return a.memory.ConfirmRule(text) }

// MemoryConfirmRules records that the person keeps several rules (один Touch ID).
func (a *App) MemoryConfirmRules(texts []string) error { return a.memory.ConfirmRules(texts) }

// MemoryIndex rebuilds the vector index of the cold layer through the local Ollama.
func (a *App) MemoryIndex() (indexReport, error) { return a.memory.Index() }

// MemoryTopics rebuilds the topic list of the hot layer.
func (a *App) MemoryTopics() (bool, error) { return a.memory.Topics() }

// MemoryWho rewrites the «Кто я» block of the hot layer from the approved facts of
// the portrait (папка или kb-сервер — как выбрано в «Чат и модель»).
func (a *App) MemoryWho() (bool, error) {
	if a.chat == nil {
		return false, errNoPortraitDir
	}
	return a.memory.Who(portraitText(func() (string, error) {
		var text string
		err := a.chat.withPortrait(nil, func(s *portrait.Service) error {
			p, err := s.Portrait(context.Background())
			text = portrait.Text(p.Approved())
			return err
		})
		return text, portraitErr(err)
	}))
}

// Connections lists the window's accesses (раздел «Подключения»).
func (a *App) Connections() []connRow { return a.settings.Connections() }

// StartDictation starts listening; текст приходит событием dictation. pause > 0
// — разговор без рук: тишина pause секунд кончает вопрос.
func (a *App) StartDictation(pause float64) error {
	if a.listen == nil {
		return errors.New("диктовка работает только в окне приложения")
	}
	tr, err := a.settings.listenTranscriber()
	if err != nil {
		return err
	}
	return a.listen.StartWith(pause, tr)
}

// StopDictation ends listening; итог приходит событием dictation с final.
func (a *App) StopDictation() {
	if a.listen != nil {
		a.listen.Stop()
	}
}

// Speak reads an answer aloud with the voice from the chat settings; конец
// чтения приходит событием speech.
func (a *App) Speak(text string) error {
	if a.voice == nil {
		return errors.New("голос работает только в окне приложения")
	}
	c := a.chat.config()
	voice := c.Voice
	// Акцент на Маке: английский ответ читает английский голос нужного произношения.
	if !isCloudVoice(voice) {
		if lang := speechLang(speakable(text), c.Accent); lang != "" {
			if v := englishVoice(lang); v != "" {
				voice = v
			}
		}
	}
	return a.voice.Speak(voice, text)
}

// Voices lists the Russian voices of this Mac for the settings.
func (a *App) Voices() []string { return russianVoices() }

// CloudVoices lists the cloud voices for the settings (#478).
func (a *App) CloudVoices() []cloudVoice { return cloudVoices }

// PreviewVoice says a sample phrase with voice for the settings (#478).
func (a *App) PreviewVoice(voice string) error {
	if a.voice == nil {
		return errors.New("голос работает только в окне приложения")
	}
	return previewVoice(a.voice, russianVoices, voice)
}

// OpenVoiceSettings opens the macOS pane where Premium and Enhanced voices
// are downloaded.
func (a *App) OpenVoiceSettings() error { return voiceSettingsCommand().Run() }

// ListenStatus says whether dictation works here and without the network.
func (a *App) ListenStatus() listenStatus {
	if a.listen == nil {
		return listenStatus{}
	}
	return a.listen.Check()
}

// StopSpeaking interrupts the reading.
func (a *App) StopSpeaking() {
	if a.voice != nil {
		a.voice.Stop()
	}
}

// ConnectGoogle signs in to Google with the person's OAuth client: окно
// открывает страницу согласия в браузере и ждет ответа до 5 минут.
func (a *App) ConnectGoogle(clientID, secret string) (connRow, error) {
	if a.google == nil || a.google.open == nil {
		return connRow{}, errors.New("подключение Google — в окне приложения")
	}
	return a.google.Connect(clientID, secret)
}

// AddSpeechConnection saves a speech recognition server (whisper и т. п.).
func (a *App) AddSpeechConnection(name, url, model, key string) (connRow, error) {
	return a.settings.AddSpeech(name, url, model, key)
}

// SetConnectionEnabled turns a connection of the window on or off.
func (a *App) SetConnectionEnabled(id string, on bool) error { return a.google.SetEnabled(id, on) }

// RemoveConnection deletes a connection and its secrets.
func (a *App) RemoveConnection(id string) error { return a.google.Remove(id) }

// MountShare connects a network folder now (smb://… или afp://…) and returns
// its path in /Volumes: macOS спросит пароль и запомнит его в Связке ключей.
func (a *App) MountShare(addr string) (string, error) {
	vol := desktopconfig.ShareVolume(addr)
	if vol == "" {
		return "", errors.New("адрес сетевой папки — smb://сервер/папка, afp://… или https://… (WebDAV, Яндекс Диск), без пароля в адресе")
	}
	if err := mountShareCmd(addr); err != nil {
		return "", fmt.Errorf("сетевая папка не подключилась: %w", err)
	}
	p, ok := mountedVolume(addr, vol, systemMounts())
	if !ok {
		return "", fmt.Errorf("шара подключена, но не найдена в /Volumes (имя %s занято другим диском или папки нет) — проверьте адрес", vol)
	}
	return p, nil
}

// PickChatFile asks for a file to attach to the question; "" — передумали.
func (a *App) PickChatFile() (string, error) {
	return runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{Title: "Приложить к вопросу"})
}

// ChatFind searches the chat's sources for a document to attach.
func (a *App) ChatFind(q string) ([]chatDoc, error) { return a.chat.Find(q) }

// DeleteChat removes a chat.
func (a *App) DeleteChat(id string) error { return a.chat.Delete(id) }

// IndexStatus reports whether a build is running and how the last one went.
func (a *App) IndexStatus() indexStatus { return a.index.Status() }

// PickFolder opens the system folder chooser — the same one as when attaching
// files — and returns the chosen path, or "" if the person cancelled.
func (a *App) PickFolder() (string, error) {
	return runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title:                "Папка для поиска",
		CanCreateDirectories: false,
	})
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	// Кусок уходит странице весь (она перерисовывает текст целиком), поэтому
	// не чаще раза в 50 мс. Устаревший кусок после ответа прийти не может:
	// событие и ответ Ask идут в одну очередь главного потока (ExecJS →
	// dispatch_async), куски показаны до возврата Ask — значит, раньше ответа.
	if a.chat != nil {
		a.chat.show = everyAtMost(50*time.Millisecond, time.Now, func(text string) { runtime.EventsEmit(ctx, "chat:delta", text) })
		a.chat.memoryNew = func(n portraitNews) { runtime.EventsEmit(ctx, "portrait:new", n) }
	}
	if a.listen != nil {
		a.listen.emit = func(ev listenEvent) { runtime.EventsEmit(ctx, "dictation", ev) }
	}
	if a.voice != nil {
		a.voice.done = func() { runtime.EventsEmit(ctx, "speech") }
	}
	if a.google != nil {
		a.google.open = func(u string) { runtime.BrowserOpenURL(ctx, u) }
	}
}

// Open opens a found file in its default application.
//
// Путь приходит со страницы, то есть снаружи: прежде чем звать open, он
// проходит ту же проверку, что и чтение через API. Иначе окно открыло бы
// любой файл диска, включая .env, по одному вызову из консоли страницы.
func (a *App) Open(path string) error {
	if err := a.files.Check(path); err != nil {
		return err
	}
	return a.run("open", path)
}

// Reveal shows a found file in Finder, under the same check as Open.
func (a *App) Reveal(path string) error {
	if err := a.files.Check(path); err != nil {
		return err
	}
	return a.run("open", "-R", path)
}
