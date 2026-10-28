// middleware/audit_middleware.go
package middleware

import (
	"bytes"
	"encoding/json"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jeremielodi/bpmngo/internal/models"
	"github.com/jeremielodi/bpmngo/internal/repository"
	"github.com/jmoiron/sqlx"
)

type AuditMiddleware struct {
	auditRepo *repository.AuditRepository
}

func NewAuditMiddleware(db *sqlx.DB) *AuditMiddleware {
	return &AuditMiddleware{
		auditRepo: repository.NewAuditRepository(db),
	}
}

// sensitiveKeys are JSON fields never written to the audit log, at any
// depth, compared case-insensitively.
var sensitiveKeys = map[string]bool{
	"password": true, "current_password": true, "new_password": true,
	"token": true, "access_token": true, "refresh_token": true,
	"client_secret": true, "secret": true,
}

// redactBody returns a request body fit for the audit log: JSON with its
// sensitive fields replaced, or a placeholder for a body that is not JSON
// (a form may carry a password too).
func redactBody(body []byte) string {
	var v interface{}
	if err := json.Unmarshal(body, &v); err != nil {
		return "[non-JSON body not logged]"
	}
	out, _ := json.Marshal(redactValue(v))
	return string(out)
}

func redactValue(v interface{}) interface{} {
	switch t := v.(type) {
	case map[string]interface{}:
		for k, val := range t {
			if sensitiveKeys[strings.ToLower(k)] {
				t[k] = "[redacted]"
			} else {
				t[k] = redactValue(val)
			}
		}
	case []interface{}:
		for i := range t {
			t[i] = redactValue(t[i])
		}
	}
	return v
}

// APIAuditMiddleware logs API requests and responses
func (m *AuditMiddleware) APIAuditMiddleware() fiber.Handler {
	return func(c *fiber.Ctx) error {
		start := time.Now()

		// Read request body
		var requestBody []byte
		if c.Request().Body() != nil {
			requestBody = bytes.Clone(c.Request().Body())
		}

		// Process request
		err := c.Next()

		// Calculate duration
		duration := time.Since(start)

		// Log API call
		auditLog := &models.AuditLog{
			ID:        uuid.New(),
			Action:    models.AuditAction("API_CALL"),
			CreatedAt: time.Now(),
		}

		logData := map[string]interface{}{
			"method":    c.Method(),
			"path":      c.Path(),
			"status":    c.Response().StatusCode(),
			"duration":  duration.Milliseconds(),
			"ip":        c.IP(),
			"userAgent": c.Get("User-Agent"),
		}

		if len(requestBody) > 0 && len(requestBody) < 10000 { // Don't log huge bodies
			logData["requestBody"] = redactBody(requestBody)
		}

		if err != nil {
			logData["error"] = err.Error()
		}

		logDataJSON, _ := json.Marshal(logData)
		auditLog.NewData = logDataJSON

		// Async log to avoid blocking
		go m.auditRepo.CreateAuditLog(auditLog)

		return err
	}
}
