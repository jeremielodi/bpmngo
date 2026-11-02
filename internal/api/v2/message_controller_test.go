package v2

import (
	"testing"

	"github.com/google/uuid"
	"github.com/jeremielodi/bpmngo/internal/runtime"
)

func TestMessagePublicationResponseMatchesCamunda(t *testing.T) {
	messageID := uuid.New()
	body := messagePublicationResponse(runtime.PublishResult{
		MessageID:  messageID,
		Correlated: []uuid.UUID{uuid.New()},
		Started:    []uuid.UUID{uuid.New()},
	}, "")

	if body["messageKey"] != messageID || body["tenantId"] != "<default>" {
		t.Fatalf("unexpected response: %#v", body)
	}
	if len(body) != 2 {
		t.Fatalf("Camunda response must contain only messageKey and tenantId: %#v", body)
	}
}
