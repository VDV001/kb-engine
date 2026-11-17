package desktopconfig

// Images — модель изображений, подключенная человеком (#678). Ключ сюда не
// пишется никогда: он в Связке ключей.
type Images struct {
	URL   string `json:"url,omitempty"`
	Model string `json:"model,omitempty"`
}

// On reports whether an image model is connected (заглушка до GREEN).
func (i Images) On() bool { return false }

// Check validates the settings (заглушка до GREEN).
func (i Images) Check() error { return nil }
