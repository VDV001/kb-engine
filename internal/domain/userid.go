package domain

import (
	"errors"
	"fmt"
)

// ErrInvalidUserID is returned for a name that cannot identify a person on the
// server.
var ErrInvalidUserID = errors.New("invalid user id")

var (
	ErrUserExists = errors.New("user already exists")
	ErrNoSuchUser = errors.New("no such user")
)

// maxUserIDLen bounds the name: оно стоит в каждом ключе хранилища человека.
const maxUserIDLen = 32

// UserID names a person on the server. Имя становится частью ключа хранилища
// (users/<id>/…), поэтому допустимы только строчные латинские буквы, цифры и
// дефис не по краям: ни «/», ни «..», ни регистр, который на одном диске
// сливает два имени в одно, а в бакете — нет.
type UserID struct{ raw string }

// NewUserID validates s.
func NewUserID(s string) (UserID, error) {
	if s == "" || len(s) > maxUserIDLen {
		return UserID{}, fmt.Errorf("%w: %q: от 1 до %d символов", ErrInvalidUserID, s, maxUserIDLen)
	}
	for i, r := range s {
		edge := i == 0 || i == len(s)-1
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
		case r == '-' && !edge:
		default:
			return UserID{}, fmt.Errorf("%w: %q: только a-z, 0-9 и дефис не по краям", ErrInvalidUserID, s)
		}
	}
	return UserID{raw: s}, nil
}

// String returns the name.
func (u UserID) String() string { return u.raw }
