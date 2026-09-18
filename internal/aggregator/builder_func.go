package aggregator

import (
	"github.com/BROngineer/argocd-notifier/internal/event"
	"github.com/BROngineer/argocd-notifier/internal/render"
)

type MessageBuilderFunc func(perApp map[string]event.Event) (render.Message, error)

func (f MessageBuilderFunc) Build(perApp map[string]event.Event) (render.Message, error) {
	return f(perApp)
}
