package v2

import (
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jeremielodi/bpmngo/internal/api"
	"github.com/jeremielodi/bpmngo/internal/repository"
	"github.com/jmoiron/sqlx"
)

type SignalController struct {
	inner *api.SignalController
	keys  *repository.ZeebeKeyRepository
}

func NewSignalController(db *sqlx.DB, inner *api.SignalController) *SignalController {
	return &SignalController{inner: inner, keys: repository.NewZeebeKeyRepository(db)}
}

// BroadcastSignalRequest matches Camunda 8's signal broadcast body.
type BroadcastSignalRequest struct {
	SignalName string                 `json:"signalName"`
	Variables  map[string]interface{} `json:"variables"`
	TenantID   string                 `json:"tenantId"`
}

// BroadcastSignal handles POST /v2/signals/broadcast.
func (sc *SignalController) BroadcastSignal(c *fiber.Ctx) error {
	var req BroadcastSignalRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"title": "INVALID_ARGUMENT", "detail": err.Error()})
	}
	if req.SignalName == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"title": "INVALID_ARGUMENT", "detail": "signalName is required"})
	}

	_, _, err := sc.inner.Broadcast(req.SignalName, req.Variables)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"title": "INTERNAL_ERROR", "detail": err.Error()})
	}
	signalID := uuid.New()
	if _, err := sc.keys.GetOrAssignKey("signal", signalID); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"title": "INTERNAL_ERROR", "detail": err.Error()})
	}
	return c.JSON(signalBroadcastResponse(signalID, req.TenantID))
}

func signalBroadcastResponse(signalID uuid.UUID, tenantID string) fiber.Map {
	if tenantID == "" {
		tenantID = "<default>"
	}
	return fiber.Map{"signalKey": signalID, "tenantId": tenantID}
}
