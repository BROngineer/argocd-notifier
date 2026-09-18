package render

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMessage_MarshalJSON(t *testing.T) {
	t.Run("omits attachments when empty", func(t *testing.T) {
		b, err := json.Marshal(Message{Text: "hello"})
		if err != nil {
			t.Fatalf("Marshal() error = %v", err)
		}
		if strings.Contains(string(b), "attachments") {
			t.Fatalf("expected no attachments key, got %s", b)
		}
	})

	t.Run("includes attachment fields", func(t *testing.T) {
		msg := Message{
			Text: "hello",
			Attachments: []Attachment{
				{Color: "#18be52", Blocks: []map[string]any{{"type": "section"}}},
			},
		}
		b, err := json.Marshal(msg)
		if err != nil {
			t.Fatalf("Marshal() error = %v", err)
		}

		var got map[string]any
		if err := json.Unmarshal(b, &got); err != nil {
			t.Fatalf("Unmarshal() error = %v", err)
		}
		attachments, ok := got["attachments"].([]any)
		if !ok || len(attachments) != 1 {
			t.Fatalf("expected 1 attachment, got %v", got["attachments"])
		}
		attachment := attachments[0].(map[string]any)
		if attachment["color"] != "#18be52" {
			t.Fatalf("color = %v, want #18be52", attachment["color"])
		}
	})
}
