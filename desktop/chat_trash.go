package main

import (
	"errors"
	"time"
)

// trashKeep — сколько удаленный чат лежит в корзине.
const trashKeep = 7 * 24 * time.Hour

// Trash lists deleted chats.
func (d *chatDesk) Trash() ([]chatSummary, error) { return nil, errors.New("не сделано") }

// Restore returns a deleted chat.
func (d *chatDesk) Restore(string) error { return errors.New("не сделано") }
