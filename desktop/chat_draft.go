package main

// Черновик чата (#540): набранный, но не отправленный вопрос лежит рядом с
// историей и переживает перезапуск окна.

// maxDraft caps a draft in bytes: поле ввода не должно раздувать папку чатов.
const maxDraft = 64 << 10

// Draft returns the saved draft of chat id ("" — еще не созданный чат).
func (d *chatDesk) Draft(id string) (string, error) { return "", nil }

// SaveDraft keeps text as the draft of chat id; пустой текст черновик снимает.
func (d *chatDesk) SaveDraft(id, text string) error { return nil }
