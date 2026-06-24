package changelog

import (
	"bytes"
	"encoding/json"
)

const docComment = "Сгенерировано kbengine changelog из CHANGELOG.md. Не редактировать вручную — изменения вносить в CHANGELOG.md."

// MarshalJSON renders the document in the legacy changelog.json shape: ordered
// section objects and null (not "") for absent dates, matching the Python tool
// the dashboard consumed.
func (d Document) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteString(`{"_comment":`)
	writeString(&b, docComment)
	b.WriteString(`,"current_version":`)
	writeString(&b, d.CurrentVersion)
	b.WriteString(`,"current_date":`)
	writeNullable(&b, d.CurrentDate)
	b.WriteString(`,"current_tagline":`)
	writeString(&b, d.CurrentTagline)
	b.WriteString(`,"releases":[`)
	for i := range d.Releases {
		if i > 0 {
			b.WriteByte(',')
		}
		if err := writeRelease(&b, d.Releases[i]); err != nil {
			return nil, err
		}
	}
	b.WriteString(`]}`)
	return b.Bytes(), nil
}

func writeRelease(b *bytes.Buffer, r Release) error {
	b.WriteString(`{"version":`)
	writeString(b, r.Version)
	b.WriteString(`,"date":`)
	writeNullable(b, r.Date)
	b.WriteString(`,"tagline":`)
	writeString(b, r.Tagline)
	b.WriteString(`,"sections":{`)
	for i, s := range r.Sections {
		if i > 0 {
			b.WriteByte(',')
		}
		writeString(b, s.Name)
		b.WriteByte(':')
		items := s.Items
		if items == nil {
			items = []string{}
		}
		enc, err := marshalNoEscape(items)
		if err != nil {
			return err
		}
		b.Write(enc)
	}
	b.WriteString(`}}`)
	return nil
}

// writeString writes a JSON string without HTML escaping (so Cyrillic and URL
// separators stay literal).
func writeString(b *bytes.Buffer, s string) {
	enc, _ := marshalNoEscape(s)
	b.Write(enc)
}

// writeNullable writes the JSON null literal for an empty string, otherwise the
// quoted string.
func writeNullable(b *bytes.Buffer, s string) {
	if s == "" {
		b.WriteString("null")
		return
	}
	writeString(b, s)
}

func marshalNoEscape(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}
