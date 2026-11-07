package domain

import (
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
)

// Доступ к продукту (#494, решение владельца 07.11): карточка — продукт,
// среда, адрес, логин, роль, кто выдал. Пароля в карточке нет по устройству:
// он живет в Связке ключей, а карточку видят поиск окна, терминал и агент
// через MCP — все, что уходит агенту, уходит провайдеру модели.

// ErrInvalidCredential — карточка нарушает инвариант (нет продукта, логина…).
var ErrInvalidCredential = errors.New("invalid credential")

// ErrCredentialExists — доступ с таким id уже есть.
var ErrCredentialExists = errors.New("credential already exists")

// ErrCredentialNotFound — доступа с таким id нет.
var ErrCredentialNotFound = errors.New("credential not found")

// Env — среда, к которой выдан доступ.
type Env string

// Среды — как их называют на работе; прочие слова приводятся к ним в ParseEnv.
const (
	EnvProd  Env = "prod"
	EnvStage Env = "stage" // стенд, предпрод
	EnvTest  Env = "test"
	EnvDev   Env = "dev"
)

var envWords = map[string]Env{
	"prod": EnvProd, "production": EnvProd, "прод": EnvProd, "продакшн": EnvProd, "продакшен": EnvProd, "боевой": EnvProd,
	"stage": EnvStage, "staging": EnvStage, "стенд": EnvStage, "preprod": EnvStage, "предпрод": EnvStage,
	"test": EnvTest, "testing": EnvTest, "тест": EnvTest, "тестовый": EnvTest,
	"dev": EnvDev, "development": EnvDev, "разработка": EnvDev,
}

// ParseEnv reads an environment as people write it; незнакомое слово — отказ,
// а не «прочее»: прод под опечаткой потерял бы красную метку.
func ParseEnv(s string) (Env, error) {
	if e, ok := envWords[strings.ToLower(strings.TrimSpace(s))]; ok {
		return e, nil
	}
	return "", fmt.Errorf("%w: среда %q — прод, стенд, тест или dev", ErrInvalidCredential, s)
}

// Risky reports whether a mistake there hurts real people: окно красит такую
// среду красным.
func (e Env) Risky() bool { return e == EnvProd }

// CredentialDTO is a credential card as stored and typed: поля открыты, правила
// проверяет NewCredential. ID пустой — собирается из продукта, среды и логина.
type CredentialDTO struct {
	ID       string    `json:"id"`
	Product  string    `json:"product"`
	Env      Env       `json:"env"`
	URL      string    `json:"url,omitempty"`
	Host     string    `json:"host,omitempty"`
	Login    string    `json:"login"`
	Role     string    `json:"role,omitempty"`
	Rights   string    `json:"rights,omitempty"`
	IssuedBy string    `json:"issued_by,omitempty"`
	IssuedAt time.Time `json:"issued_at,omitzero"`
	Note     string    `json:"note,omitempty"`
}

// Credential is a validated access card without a password.
type Credential struct{ d CredentialDTO }

var credentialID = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// NewCredential validates a card: продукт, логин и среда обязательны, адрес
// или хост — хотя бы одно; id — латиница, цифры и дефис, до 64 знаков.
func NewCredential(d CredentialDTO) (Credential, error) {
	for _, f := range []*string{&d.ID, &d.Product, &d.URL, &d.Host, &d.Login, &d.Role, &d.Rights, &d.IssuedBy, &d.Note} {
		*f = strings.TrimSpace(*f)
	}
	switch {
	case d.Product == "":
		return Credential{}, fmt.Errorf("%w: нет продукта", ErrInvalidCredential)
	case d.Login == "":
		return Credential{}, fmt.Errorf("%w: нет логина", ErrInvalidCredential)
	case d.URL == "" && d.Host == "":
		return Credential{}, fmt.Errorf("%w: нет ни адреса, ни хоста", ErrInvalidCredential)
	}
	env, err := ParseEnv(string(d.Env))
	if err != nil {
		return Credential{}, err
	}
	d.Env = env
	if d.ID == "" {
		d.ID = slug(d.Product) + "-" + string(env) + "-" + slug(d.Login)
	}
	d.ID = strings.ToLower(d.ID)
	if !credentialID.MatchString(d.ID) {
		return Credential{}, fmt.Errorf("%w: id %q — латиница, цифры и дефис, до 64 знаков", ErrInvalidCredential, d.ID)
	}
	return Credential{d: d}, nil
}

// slug is a name in Latin letters, digits and dashes: «Склад-Про» → sklad-pro.
func slug(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range Translit(strings.ToLower(s)) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.TrimSuffix(b.String(), "-")
}

// ID is the stable name of the access: sklad-pro-prod-admin-ops.
func (c Credential) ID() string { return c.d.ID }

// Product is what the access opens.
func (c Credential) Product() string { return c.d.Product }

// Env is the environment.
func (c Credential) Env() Env { return c.d.Env }

// URL is the address to sign in at.
func (c Credential) URL() string { return c.d.URL }

// Host is the server address.
func (c Credential) Host() string { return c.d.Host }

// Login is the account name.
func (c Credential) Login() string { return c.d.Login }

// Role is what the account is.
func (c Credential) Role() string { return c.d.Role }

// Rights is what the account may do.
func (c Credential) Rights() string { return c.d.Rights }

// IssuedBy is who gave the access.
func (c Credential) IssuedBy() string { return c.d.IssuedBy }

// IssuedAt is when the access was given.
func (c Credential) IssuedAt() time.Time { return c.d.IssuedAt }

// Note is a free remark.
func (c Credential) Note() string { return c.d.Note }

// DTO returns the card for storage and display.
func (c Credential) DTO() CredentialDTO { return c.d }

// envSay — как среду называют по-русски: поиск «склад прод» находит prod.
var envSay = map[Env]string{EnvProd: "прод", EnvStage: "стенд предпрод", EnvTest: "тест", EnvDev: "разработка"}

// Matches reports whether every word of q is somewhere in the card, без
// регистра; пустой запрос подходит всем.
func (c Credential) Matches(q string) bool {
	d := c.d
	text := strings.ToLower(strings.Join([]string{d.ID, d.Product, string(d.Env), envSay[d.Env], d.URL, d.Host, d.Login, d.Role, d.Rights, d.IssuedBy, d.Note}, " "))
	for w := range strings.FieldsSeq(strings.ToLower(q)) {
		if !strings.Contains(text, w) {
			return false
		}
	}
	return true
}

// Password is the secret of an access. Не печатает себя ни одним глаголом fmt
// (%v, %+v, %#v, %s, %q, %x): случайный лог или текст ошибки его не выдаст.
// Значение — в замыкании, а не в строковом поле: в неэкспортируемом поле
// чужой структуры fmt читает отражением мимо Format, и строка вышла бы
// наружу, а у функции отражение покажет только адрес.
// Значение — только через Reveal, и только туда, где его видит человек сам.
type Password struct{ v func() string }

// NewPassword rejects an empty password.
func NewPassword(s string) (Password, error) {
	if strings.TrimSpace(s) == "" {
		return Password{}, fmt.Errorf("%w: нет пароля", ErrInvalidCredential)
	}
	return Password{v: func() string { return s }}, nil
}

// Reveal returns the secret itself.
func (p Password) Reveal() string {
	if p.v == nil {
		return ""
	}
	return p.v()
}

// Format prints dots for every verb.
func (Password) Format(f fmt.State, _ rune) { _, _ = io.WriteString(f, "••••••") }
