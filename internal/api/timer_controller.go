package api

import (
	"strconv"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jeremielodi/bpmngo/internal/models"
	"github.com/jmoiron/sqlx"
)

type TimerController struct {
	db *sqlx.DB
}

func NewTimerController(db *sqlx.DB) *TimerController {
	return &TimerController{db: db}
}

// GetTimers handles GET /timers - returns timers for a process instance
func (c *TimerController) GetTimers(ctx *fiber.Ctx) error {
	processInstanceID := ctx.Query("processInstanceId")

	timers := []models.TimerJob{}
	query := `
		SELECT t.engine_key, pi.id AS process_instance_id, t.execution_key, event_type, due_at, payload,
		       is_triggered, created_at, triggered_at, cycle_count, total_cycles, repeat_interval
		FROM public.timer_jobs t
		LEFT JOIN public.process_instances pi ON pi.engine_key = t.process_instance_key
		WHERE 1=1
	`

	if processInstanceID != "" {
		pid, err := uuid.Parse(processInstanceID)
		if err != nil {
			return ctx.Status(400).JSON(fiber.Map{
				"error": "Invalid processInstanceId",
			})
		}
		query += " AND pi.id = $1 ORDER BY created_at DESC"
		err = c.db.Select(&timers, query, pid)
		if err != nil {
			return ctx.Status(500).JSON(fiber.Map{
				"error": err.Error(),
			})
		}
	} else {
		query += " ORDER BY created_at DESC"
		err := c.db.Select(&timers, query)
		if err != nil {
			return ctx.Status(500).JSON(fiber.Map{
				"error": err.Error(),
			})
		}
	}

	return ctx.JSON(timers)
}

// GetTimerByID handles GET /timers/:id - returns a specific timer
func (c *TimerController) GetTimerByID(ctx *fiber.Ctx) error {
	timerID, err := strconv.ParseInt(ctx.Params("id"), 10, 64)
	if err != nil {
		return ctx.Status(400).JSON(fiber.Map{
			"error": "Invalid timer ID",
		})
	}

	var timer models.TimerJob
	query := `
		SELECT t.engine_key, pi.id AS process_instance_id, t.execution_key, event_type, due_at, payload,
		       is_triggered, created_at, triggered_at, cycle_count, total_cycles, repeat_interval
		FROM public.timer_jobs t
		LEFT JOIN public.process_instances pi ON pi.engine_key = t.process_instance_key
		WHERE t.engine_key = $1
	`

	err = c.db.Get(&timer, query, timerID)
	if err != nil {
		return ctx.Status(404).JSON(fiber.Map{
			"error": "Timer not found",
		})
	}

	return ctx.JSON(timer)
}
