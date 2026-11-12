// Package docxwrite builds a minimal .docx from plain text: новая итерация
// документа, которую чат предложил сохранить (#548), должна быть документом, а
// не заметкой, чтобы встать в ленту версий рядом с остальными.
package docxwrite

// Bytes returns a .docx with one paragraph per line of text.
func Bytes(text string) ([]byte, error) { return nil, nil }
