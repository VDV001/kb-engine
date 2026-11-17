package domain

// ModelRole is which of the two chat models a question goes to (#678).
type ModelRole string

const (
	RoleMain   ModelRole = "main"
	RoleSecond ModelRole = "second"
)

// HintReason is why the hint suggests a role: код, а не текст — текст сообщения
// у окна.
type HintReason string

const (
	ReasonShort    HintReason = "short"
	ReasonLong     HintReason = "long"
	ReasonCode     HintReason = "code"
	ReasonAnalysis HintReason = "analysis"
	ReasonFiles    HintReason = "files"
)

// ModelHint is the advice for a question; пустая роль — совета нет.
type ModelHint struct {
	Role   ModelRole
	Reason HintReason
}

// SuggestModelRole advises a model for the question by rules: stub до GREEN.
func SuggestModelRole(question string, files int) ModelHint { return ModelHint{} }
