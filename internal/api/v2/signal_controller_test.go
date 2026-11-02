package v2

import (
	"testing"

	"github.com/google/uuid"
)

func TestSignalBroadcastResponseMatchesCamunda(t *testing.T) {
	id := uuid.New()
	body := signalBroadcastResponse(id, "")
	if body["signalKey"] != id || body["tenantId"] != "<default>" || len(body) != 2 {
		t.Fatalf("unexpected response: %#v", body)
	}
}
