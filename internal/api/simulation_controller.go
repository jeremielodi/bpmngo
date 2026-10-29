package api

import (
	"context"
	"database/sql"
	"errors"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jeremielodi/bpmngo/internal/models"
	"github.com/jeremielodi/bpmngo/internal/repository"
	"github.com/jeremielodi/bpmngo/internal/service"
	"github.com/jeremielodi/bpmngo/internal/simulation"
	"github.com/jeremielodi/bpmngo/pkg/common"
	"github.com/jmoiron/sqlx"
)

type SimulationController struct {
	repository *repository.SimulationRepository
	service    *service.SimulationService
}

func NewSimulationController(db *sqlx.DB) *SimulationController {
	repo := repository.NewSimulationRepository(db)
	return &SimulationController{repository: repo, service: service.NewSimulationService(repo)}
}

func (ctrl *SimulationController) StartWorkers(ctx context.Context, count int) error {
	return ctrl.service.StartWorkers(ctx, count)
}

func (ctrl *SimulationController) CreateScenario(c *fiber.Ctx) error {
	var input simulation.Scenario
	if err := c.BodyParser(&input); err != nil {
		return badRequest(c, err)
	}
	row, err := ctrl.service.CreateScenario(input, currentUserID(c), common.GetTenantId(c))
	if err != nil {
		return badRequest(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(row)
}

func (ctrl *SimulationController) ListScenarios(c *fiber.Ctx) error {
	rows, err := ctrl.repository.ListScenarios(common.GetTenantId(c))
	if err != nil {
		return internalError(c, err)
	}
	return c.JSON(rows)
}

func (ctrl *SimulationController) GetScenario(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return badRequest(c, err)
	}
	row, err := ctrl.repository.GetScenario(id, common.GetTenantId(c))
	if err != nil {
		return repositoryError(c, err)
	}
	return c.JSON(row)
}

func (ctrl *SimulationController) UpdateScenario(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return badRequest(c, err)
	}
	var input simulation.Scenario
	if err = c.BodyParser(&input); err != nil {
		return badRequest(c, err)
	}
	row, err := ctrl.service.UpdateScenario(id, input, common.GetTenantId(c))
	if err != nil {
		return mutationError(c, err)
	}
	return c.JSON(row)
}

func (ctrl *SimulationController) DeleteScenario(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return badRequest(c, err)
	}
	if err = ctrl.repository.DeleteScenario(id, common.GetTenantId(c)); err != nil {
		return internalError(c, err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (ctrl *SimulationController) Analyze(c *fiber.Ctx) error {
	var input simulation.Scenario
	if err := c.BodyParser(&input); err != nil {
		return badRequest(c, err)
	}
	result, err := simulation.Analyze(input)
	if err != nil {
		return badRequest(c, err)
	}
	return c.JSON(result)
}

func (ctrl *SimulationController) Simulate(c *fiber.Ctx) error {
	var input simulation.Scenario
	if err := c.BodyParser(&input); err != nil {
		return badRequest(c, err)
	}
	result, err := simulation.Simulate(input)
	if err != nil {
		return badRequest(c, err)
	}
	return c.JSON(result)
}

func (ctrl *SimulationController) Staffing(c *fiber.Ctx) error {
	var input simulation.StaffingRequest
	if err := c.BodyParser(&input); err != nil {
		return badRequest(c, err)
	}
	result, err := simulation.CalculateStaffing(input)
	if err != nil {
		return badRequest(c, err)
	}
	return c.JSON(result)
}

func (ctrl *SimulationController) RunScenario(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return badRequest(c, err)
	}
	run, err := ctrl.service.EnqueueSimulation(id, currentUserID(c), common.GetTenantId(c))
	if err != nil {
		return repositoryError(c, err)
	}
	return c.Status(fiber.StatusAccepted).JSON(run)
}

func (ctrl *SimulationController) RunCapacitySweep(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return badRequest(c, err)
	}
	var request simulation.CapacitySweepRequest
	if err = c.BodyParser(&request); err != nil {
		return badRequest(c, err)
	}
	run, err := ctrl.service.EnqueueCapacitySweep(id, currentUserID(c), common.GetTenantId(c), request)
	if err != nil {
		return mutationError(c, err)
	}
	return c.Status(fiber.StatusAccepted).JSON(run)
}

func (ctrl *SimulationController) ListRuns(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return badRequest(c, err)
	}
	runs, err := ctrl.repository.ListRuns(id, common.GetTenantId(c))
	if err != nil {
		return internalError(c, err)
	}
	return c.JSON(runs)
}

func (ctrl *SimulationController) GetRun(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("runId"))
	if err != nil {
		return badRequest(c, err)
	}
	run, err := ctrl.repository.GetRun(id, common.GetTenantId(c))
	if err != nil {
		return repositoryError(c, err)
	}
	return c.JSON(run)
}

func (ctrl *SimulationController) CancelRun(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("runId"))
	if err != nil {
		return badRequest(c, err)
	}
	run, err := ctrl.repository.CancelRun(id, common.GetTenantId(c))
	if err != nil {
		return repositoryError(c, err)
	}
	return c.JSON(run)
}

func (ctrl *SimulationController) ListIssues(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return badRequest(c, err)
	}
	issues, err := ctrl.repository.ListIssues(id, common.GetTenantId(c))
	if err != nil {
		return internalError(c, err)
	}
	return c.JSON(issues)
}

func (ctrl *SimulationController) CreateIssue(c *fiber.Ctx) error {
	scenarioID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return badRequest(c, err)
	}
	var issue models.SimulationIssue
	if err = c.BodyParser(&issue); err != nil {
		return badRequest(c, err)
	}
	issue.ScenarioID = scenarioID
	if issue.Code == "" {
		issue.Code = "MANUAL"
	}
	if issue.Severity == "" {
		issue.Severity = "medium"
	}
	if issue.Status == "" {
		issue.Status = "open"
	}
	row, err := ctrl.repository.CreateIssue(issue, common.GetTenantId(c))
	if err != nil {
		return mutationError(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(row)
}

func (ctrl *SimulationController) UpdateIssue(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("issueId"))
	if err != nil {
		return badRequest(c, err)
	}
	var issue models.SimulationIssue
	if err = c.BodyParser(&issue); err != nil {
		return badRequest(c, err)
	}
	row, err := ctrl.repository.UpdateIssue(id, issue, common.GetTenantId(c))
	if err != nil {
		return mutationError(c, err)
	}
	return c.JSON(row)
}

func (ctrl *SimulationController) DeleteIssue(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("issueId"))
	if err != nil {
		return badRequest(c, err)
	}
	if err = ctrl.repository.DeleteIssue(id, common.GetTenantId(c)); err != nil {
		return internalError(c, err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func currentUserID(c *fiber.Ctx) uuid.UUID { id, _ := uuid.Parse(common.GetUserUuid(c)); return id }
func badRequest(c *fiber.Ctx, err error) error {
	return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"message": err.Error()})
}
func internalError(c *fiber.Ctx, err error) error {
	return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"message": "internal error", "error": err.Error()})
}
func repositoryError(c *fiber.Ctx, err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"message": "not found"})
	}
	return internalError(c, err)
}
func mutationError(c *fiber.Ctx, err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return repositoryError(c, err)
	}
	return badRequest(c, err)
}
