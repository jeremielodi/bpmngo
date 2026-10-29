package api

import (
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jeremielodi/bpmngo/internal/models"
	"github.com/jeremielodi/bpmngo/internal/service"
	"github.com/jeremielodi/bpmngo/pkg/common"
	"github.com/jmoiron/sqlx"
)

type MultiInstanceController struct {
	db *sqlx.DB
}

func NewMultiInstanceController(db *sqlx.DB) *MultiInstanceController {
	return &MultiInstanceController{db: db}
}

// internal/api/multi_instance_controller.go
func (c *MultiInstanceController) GetByProcessInstance(ctx *fiber.Ctx) error {
	instanceId := ctx.Params("instanceId")
	pid, err := uuid.Parse(instanceId)
	if err != nil {
		return ctx.Status(400).JSON(fiber.Map{"error": "invalid id"})
	}
	if userID, perr := uuid.Parse(common.GetUserUuid(ctx)); perr == nil {
		if allowed, _ := service.CanAccessProcessInstance(c.db, userID, pid, "VIEW"); !allowed {
			return ctx.Status(404).JSON(fiber.Map{"error": "not found"})
		}
	}

	var miExec models.MultiInstanceExecution
	query := `SELECT mi.*, pi.id AS process_instance_id FROM public.multi_instance_executions mi
	          JOIN public.process_instances pi ON pi.engine_key = mi.process_instance_key
	          WHERE pi.id = $1 ORDER BY mi.created_at DESC LIMIT 1`
	err = c.db.Get(&miExec, query, pid)
	if err != nil {
		return ctx.Status(404).JSON(fiber.Map{"error": err.Error()})
	}

	var children []models.MultiInstanceChild
	query2 := `SELECT * FROM public.multi_instance_children WHERE parent_execution_key = $1`
	c.db.Select(&children, query2, miExec.ExecutionID)

	return ctx.JSON(fiber.Map{
		"execution": miExec,
		"children":  children,
	})
}
