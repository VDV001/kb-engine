package domain

import "errors"

var ErrInvalidUserID = errors.New("invalid user id")

type UserID struct{ raw string }

func NewUserID(s string) (UserID, error) { return UserID{raw: s}, nil }

func (u UserID) String() string { return u.raw }
