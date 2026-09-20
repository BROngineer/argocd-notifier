package slackbackend

import (
	"github.com/BROngineer/argocd-notifier/api/backendapi"
	"github.com/BROngineer/argocd-notifier/internal/notification"
)

// fromWireNotification/fromWireItem are the mirror of
// remotebackend.toWireNotification/toWireItem: wire types stay separate
// from the domain model on this side of the boundary too.

func fromWireNotification(n backendapi.Notification) notification.Notification {
	items := make([]notification.Item, len(n.Items))
	for i, item := range n.Items {
		items[i] = fromWireItem(item)
	}
	return notification.Notification{Summary: n.Summary, Items: items}
}

func fromWireItem(item backendapi.Item) notification.Item {
	domain := notification.Item{
		AppName: item.AppName,
		Cluster: item.Cluster,
		Trigger: item.Trigger,
	}
	if item.CommitSHA != nil {
		domain.CommitSHA = *item.CommitSHA
	}
	if item.CommitURL != nil {
		domain.CommitURL = *item.CommitURL
	}
	if item.DetailText != nil {
		domain.DetailText = *item.DetailText
	}
	if item.Link != nil {
		domain.Link = *item.Link
	}
	if item.TriggeredBy != nil {
		domain.TriggeredBy = *item.TriggeredBy
	}
	if item.Images != nil {
		domain.Images = *item.Images
	}
	if item.Fields != nil {
		fields := make([]notification.Field, len(*item.Fields))
		for i, f := range *item.Fields {
			fields[i] = notification.Field{Label: f.Label, Value: f.Value}
		}
		domain.Fields = fields
	}
	return domain
}
