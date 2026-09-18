package render

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/BROngineer/argocd-notifier/internal/event"
)

var ErrNoEvents = errors.New("NoEvents")

const maxAttachments = 50

func BuildMessage(perApp map[string]event.Event) (Message, error) {
	if len(perApp) == 0 {
		return Message{}, ErrNoEvents
	}

	names := make([]string, 0, len(perApp))
	counts := make(map[string]int, len(perApp))
	var groupKey string
	for name, ev := range perApp {
		names = append(names, name)
		counts[ev.Trigger]++
		groupKey = ev.GroupKey
	}
	sort.Strings(names)

	attachments := make([]Attachment, 0, len(names))
	for _, name := range names {
		build, ok := triggerBuilders[perApp[name].Trigger]
		if !ok {
			continue
		}
		attachments = append(attachments, build(perApp[name]))
	}

	if len(attachments) > maxAttachments {
		overflow := len(attachments) - (maxAttachments - 1)
		attachments = append(attachments[:maxAttachments-1], moreAttachment(overflow))
	}

	return Message{
		Text:        summaryText(groupKey, counts),
		Attachments: attachments,
	}, nil
}

func summaryText(groupKey string, counts map[string]int) string {
	triggers := make([]string, 0, len(counts))
	for t := range counts {
		triggers = append(triggers, t)
	}
	sort.Strings(triggers)

	parts := make([]string, 0, len(triggers))
	for _, t := range triggers {
		parts = append(parts, fmt.Sprintf("%d %s", counts[t], strings.TrimPrefix(t, "on-")))
	}

	return fmt.Sprintf("%s — %s", groupKey, strings.Join(parts, ", "))
}

func moreAttachment(overflow int) Attachment {
	return Attachment{
		Color: "#9b9b9b",
		Blocks: []map[string]any{
			{
				"type": "section",
				"text": mrkdwnText(fmt.Sprintf("_+%d more_", overflow)),
			},
		},
	}
}
