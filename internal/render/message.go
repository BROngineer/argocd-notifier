package render

type Attachment struct {
	Color  string           `json:"color"`
	Blocks []map[string]any `json:"blocks"`
}

type Message struct {
	Text        string       `json:"text"`
	Attachments []Attachment `json:"attachments,omitempty"`
}
