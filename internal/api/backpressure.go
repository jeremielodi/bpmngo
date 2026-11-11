package api

import (
	"github.com/gofiber/fiber/v2"
	"github.com/jeremielodi/bpmngo/internal/metrics"
	"github.com/jeremielodi/bpmngo/internal/runtime"
)

// Backpressure guards the REST routes that create new work (start an
// instance, publish or correlate a message, broadcast a signal): while the
// engine is overloaded (see runtime.AdmitNewWork) they answer 503 with
// Retry-After instead of queueing until the client gives up. Routes that
// complete accepted work are not wrapped.
func Backpressure(c *fiber.Ctx) error {
	if err := runtime.AdmitNewWork(); err != nil {
		metrics.BackpressureRejections.WithLabelValues("rest").Inc()
		c.Set(fiber.HeaderRetryAfter, "1")
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
			"type":   "about:blank",
			"title":  "RESOURCE_EXHAUSTED",
			"status": fiber.StatusServiceUnavailable,
			"detail": err.Error(),
		})
	}
	return c.Next()
}
