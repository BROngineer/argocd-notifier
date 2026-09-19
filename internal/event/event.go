package event

import "errors"

var (
	ErrMissingGroupKey  = errors.New("MissingGroupKey")
	ErrMissingAppName   = errors.New("MissingAppName")
	ErrMissingTrigger   = errors.New("MissingTrigger")
	ErrMissingRevision  = errors.New("MissingRevision")
	ErrMissingRecipient = errors.New("MissingRecipient")
	ErrMissingBackend   = errors.New("MissingBackend")
)

// Event mirrors the JSON body ArgoCD's notifications-engine webhook service
// renders per Application trigger firing — this is the wire contract with
// ArgoCD, not an internal type, so field names/shape must stay in sync with
// the webhook template on the ArgoCD side. Fields without `omitempty` are
// required; Validate enforces exactly that set.
type Event struct {
	GroupKey     string            `json:"groupKey"`
	AppName      string            `json:"appName"`
	Trigger      string            `json:"trigger"`
	Revision     string            `json:"revision"`
	Recipient    string            `json:"recipient"`
	Backend      string            `json:"backend"`
	Labels       map[string]string `json:"labels,omitempty"`
	Target       string            `json:"target,omitempty"`
	HealthStatus string            `json:"healthStatus,omitempty"`
	SyncPhase    string            `json:"syncPhase,omitempty"`
	SyncStatus   string            `json:"syncStatus,omitempty"`
	OperationMsg string            `json:"operationMessage,omitempty"`
	RepoURL      string            `json:"repoURL,omitempty"`
	Images       []string          `json:"images,omitempty"`
	InitiatedBy  string            `json:"initiatedBy,omitempty"`
	ArgoCDURL    string            `json:"argocdUrl,omitempty"`
}

func (e Event) Validate() error {
	var errs []error
	if e.GroupKey == "" {
		errs = append(errs, ErrMissingGroupKey)
	}
	if e.AppName == "" {
		errs = append(errs, ErrMissingAppName)
	}
	if e.Trigger == "" {
		errs = append(errs, ErrMissingTrigger)
	}
	if e.Revision == "" {
		errs = append(errs, ErrMissingRevision)
	}
	if e.Recipient == "" {
		errs = append(errs, ErrMissingRecipient)
	}
	if e.Backend == "" {
		errs = append(errs, ErrMissingBackend)
	}
	return errors.Join(errs...)
}
