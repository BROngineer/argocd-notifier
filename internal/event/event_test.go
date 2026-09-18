package event

import (
	"errors"
	"testing"
)

func validEvent() Event {
	return Event{
		GroupKey:  "tatooine",
		AppName:   "tatooine-dev-empire",
		Trigger:   "on-deployed",
		Revision:  "abc123",
		Recipient: "test1234asdf",
	}
}

func TestEvent_Validate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(e *Event)
		wantErr error
	}{
		{name: "valid", mutate: func(e *Event) {}, wantErr: nil},
		{name: "missing group key", mutate: func(e *Event) { e.GroupKey = "" }, wantErr: ErrMissingGroupKey},
		{name: "missing app name", mutate: func(e *Event) { e.AppName = "" }, wantErr: ErrMissingAppName},
		{name: "missing trigger", mutate: func(e *Event) { e.Trigger = "" }, wantErr: ErrMissingTrigger},
		{name: "missing revision", mutate: func(e *Event) { e.Revision = "" }, wantErr: ErrMissingRevision},
		{name: "missing recipient", mutate: func(e *Event) { e.Recipient = "" }, wantErr: ErrMissingRecipient},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ev := validEvent()
			tt.mutate(&ev)
			err := ev.Validate()
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Validate() = %v, want errors.Is(_, %v)", err, tt.wantErr)
			}
		})
	}
}

func TestEvent_Validate_MultipleMissingFields(t *testing.T) {
	err := Event{}.Validate()
	for _, want := range []error{
		ErrMissingGroupKey,
		ErrMissingAppName,
		ErrMissingTrigger,
		ErrMissingRevision,
		ErrMissingRecipient,
	} {
		if !errors.Is(err, want) {
			t.Errorf("Validate() = %v, want errors.Is(_, %v)", err, want)
		}
	}
}
