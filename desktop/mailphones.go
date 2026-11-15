package main

// Телефоны чата (#622): привязка QR, список, отвязка. Заготовка без поведения.

// phonePairing is a pairing the window shows.
type phonePairing struct {
	Device string `json:"device"`
	QR     string `json:"qr"`
}

// phoneRow is a paired phone in the list.
type phoneRow struct {
	Device string `json:"device"`
	At     string `json:"at"`
}

func mailKeyAccount(id string) string { return "" }

// MailPairPhone pairs a phone.
func (f *folderSettings) MailPairPhone(string) (phonePairing, error) { return phonePairing{}, nil }

// MailPhones lists phones.
func (f *folderSettings) MailPhones(string) ([]phoneRow, error) { return nil, nil }

// MailRevokePhone unpairs a phone.
func (f *folderSettings) MailRevokePhone(string, string) error { return nil }
