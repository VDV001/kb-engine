package main

import "errors"

// RenameChat sets the title of chat id: человек называет чат сам, вместо первых
// слов вопроса. Время последнего ответа не меняется: переименование не двигает
// чат в списке. Под замком чатов, как Ask: иначе ответ, пришедший во время
// переименования, записал бы прежнее название поверх нового.
func (d *chatDesk) RenameChat(id, name string) error {
	name = title(name)
	if name == "" {
		return errors.New("название не может быть пустым")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	rec, err := d.load(id)
	if err != nil {
		return err
	}
	rec.Title = name
	return d.save(rec)
}
