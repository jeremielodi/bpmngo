package api

import (
	"strconv"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jeremielodi/bpmngo/internal/models"
	"github.com/jmoiron/sqlx"
)

type JobController struct {
	db *sqlx.DB
}

func NewJobController(db *sqlx.DB) *JobController {
	return &JobController{db: db}
}

func (c *JobController) GetJobs(ctx *fiber.Ctx) error {
	processInstanceID := ctx.Query("processInstanceId")
	jobType := ctx.Query("jobType")

	var jobs []models.Job
	query := `SELECT j.engine_key, pi.id AS process_instance_id, j.execution_key, j.job_type, j.status, j.payload, j.retries, j.error_message, j.locked_by, j.locked_until, j.created_at, j.completed_at, j.updated_at
	          FROM public.jobs j JOIN public.process_instances pi ON pi.engine_key = j.process_instance_key WHERE 1=1`
	args := []interface{}{}
	argIndex := 1

	if processInstanceID != "" {
		pid, err := uuid.Parse(processInstanceID)
		if err == nil {
			query += " AND pi.id = $" + strconv.Itoa(argIndex)
			args = append(args, pid)
			argIndex++
		}
	}

	if jobType != "" {
		query += " AND j.job_type = $" + strconv.Itoa(argIndex)
		args = append(args, jobType)
		argIndex++
	}

	query += " ORDER BY j.created_at DESC"

	err := c.db.Select(&jobs, query, args...)
	if err != nil {
		return ctx.Status(500).JSON(fiber.Map{
			"title": "Database Error",
			"error": err.Error(),
		})
	}

	return ctx.JSON(jobs)
}
