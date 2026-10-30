package v2

import (
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jeremielodi/bpmngo/internal/engine"
	"github.com/jeremielodi/bpmngo/internal/models"
	"github.com/jeremielodi/bpmngo/internal/repository"
	"github.com/jmoiron/sqlx"
)

type FlownodeInstanceController struct {
	db           *sqlx.DB
	instanceRepo *repository.ProcessInstanceRepository
}

func NewFlownodeInstanceController(db *sqlx.DB) *FlownodeInstanceController {
	return &FlownodeInstanceController{db: db, instanceRepo: repository.NewProcessInstanceRepository(db)}
}

// SearchFlownodeInstancesRequest matches the (simplified) Camunda 8 search body.
type SearchFlownodeInstancesRequest struct {
	Filter struct {
		ProcessInstanceKey string `json:"processInstanceKey"`
	} `json:"filter"`
	Page struct {
		Limit int `json:"limit"`
	} `json:"page"`
}

// flownodeInstanceState maps an execution's (status, is_active) pair to the
// Camunda 8 flow-node-instance state naming. Completed executions are never
// deleted (only marked is_active=false), so this doubles as flow-node
// instance history, not just the currently active tokens.
func flownodeInstanceState(status string, isActive bool) string {
	if isActive {
		return "ACTIVE"
	}
	switch status {
	case "completed":
		return "COMPLETED"
	case "terminated":
		return "TERMINATED"
	default:
		return "COMPLETED"
	}
}

func (fc *FlownodeInstanceController) executionItem(e repository.Execution) fiber.Map {
	var endDate interface{}
	if !e.IsActive {
		endDate = e.UpdatedAt
	}
	item := fiber.Map{
		"elementInstanceKey":     e.ID,
		"processInstanceKey":     e.ProcessInstanceID,
		"rootProcessInstanceKey": e.ProcessInstanceID,
		"elementId":              e.CurrentElementID,
		"state":                  flownodeInstanceState(e.Status, e.IsActive),
		"startDate":              e.CreatedAt,
		"endDate":                endDate,
		"hasIncident":            false,
		"incidentKey":            nil,
		"tenantId":               "<default>",
	}
	fc.enrichElementItem(item, e.ProcessInstanceID, e.CurrentElementID)
	return item
}

func (fc *FlownodeInstanceController) enrichElementItem(item fiber.Map, instanceID uuid.UUID, elementID string) {
	instanceRepo := repository.NewProcessInstanceRepository(fc.db)
	inst, _ := instanceRepo.FindByID(instanceID)
	if inst == nil {
		inst, _ = instanceRepo.FindHistoricByID(instanceID)
	}
	if inst == nil {
		return
	}
	item["processDefinitionId"] = inst.ProcessKey
	item["processDefinitionKey"] = inst.ProcessDefinitionID
	if inst.TenantID != nil && *inst.TenantID != "" {
		item["tenantId"] = *inst.TenantID
	}
	if graph, err := repository.NewEngineRepository(fc.db, nil).GetProcessGraphByDefinitionID(inst.ProcessDefinitionID); err == nil && graph != nil {
		if node, ok := graph.Nodes[elementID]; ok {
			item["elementName"] = node.Name
			if node.Name == "" {
				item["elementName"] = node.ID
			}
			item["type"] = camundaElementType(node.Type)
		}
	}
}

func camundaElementType(t engine.NodeType) string {
	switch t {
	case engine.StartEventType, engine.StartTimerEventType, engine.MessageStartEventType, engine.SignalStartEventType:
		return "START_EVENT"
	case engine.EndEventType, engine.TerminateEndEventType, engine.ErrorEndEventType, engine.SignalEndEventType, engine.EscalationEndEventType, engine.CompensationEndEventType:
		return "END_EVENT"
	case engine.ServiceTaskType:
		return "SERVICE_TASK"
	case engine.UserTaskType:
		return "USER_TASK"
	case engine.ExclusiveGatewayType:
		return "EXCLUSIVE_GATEWAY"
	case engine.InclusiveGatewayType:
		return "INCLUSIVE_GATEWAY"
	case engine.ParallelGatewayType:
		return "PARALLEL_GATEWAY"
	case engine.CallActivityType:
		return "CALL_ACTIVITY"
	case engine.SubProcessType, engine.EventSubProcessType:
		return "SUB_PROCESS"
	default:
		return string(t)
	}
}

// SearchFlownodeInstances handles POST /v2/element-instances/search (Camunda
// 8.8+) and its older name /v2/flownode-instances/search. Without a
// processInstanceKey filter it lists the most recent element instances the
// caller may view.
func (fc *FlownodeInstanceController) SearchFlownodeInstances(c *fiber.Ctx) error {
	var req SearchFlownodeInstancesRequest
	_ = c.BodyParser(&req)
	access := newInstanceAccess(c, fc.db, "VIEW")

	if req.Filter.ProcessInstanceKey == "" {
		limit := req.Page.Limit
		if limit <= 0 || limit > 1000 {
			limit = 100
		}
		executions, err := fc.instanceRepo.FindRecentExecutions(limit)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"title": "INTERNAL_ERROR", "detail": err.Error()})
		}
		items := make([]fiber.Map, 0, len(executions))
		for _, e := range executions {
			if access.allowed(e.ProcessInstanceID) {
				items = append(items, fc.executionItem(e))
			}
		}
		return c.JSON(elementSearchResponse(items))
	}

	instanceID, err := resolveInstanceKey(fc.db, req.Filter.ProcessInstanceKey)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"title": "INVALID_ARGUMENT", "detail": "invalid processInstanceKey",
		})
	}

	if !access.allowed(instanceID) {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"title": "NOT_FOUND", "detail": "process instance not found",
		})
	}

	executions, err := fc.instanceRepo.FindExecutionsByInstance(instanceID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"title": "INTERNAL_ERROR", "detail": err.Error()})
	}
	if len(executions) == 0 {
		// An archived instance: its executions are gone, its history is not.
		items, err := fc.historicItems(instanceID)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"title": "INTERNAL_ERROR", "detail": err.Error()})
		}
		return c.JSON(elementSearchResponse(items))
	}

	items := make([]fiber.Map, 0, len(executions))
	for _, e := range executions {
		items = append(items, fc.executionItem(e))
	}
	return c.JSON(elementSearchResponse(items))
}

func elementSearchResponse(items []fiber.Map) fiber.Map {
	return fiber.Map{"items": items, "page": searchPage(items, "elementInstanceKey")}
}

// historicItems rebuilds an archived instance's element instances from its
// token history: each element a token entered is one element instance,
// ended when the token moved on (or when the instance ended).
func (fc *FlownodeInstanceController) historicItems(instanceID uuid.UUID) ([]fiber.Map, error) {
	archive := repository.NewHistoricArchiveRepository(fc.db)
	rows, err := archive.FindActivityInstancesByProcessInstance(instanceID)
	if err != nil {
		return nil, err
	}
	endState := "COMPLETED"
	if h, err := archive.FindProcessInstanceByID(instanceID); err == nil && h != nil && h.State == "terminated" {
		endState = "TERMINATED"
	}

	items := []fiber.Map{}
	open := map[int64]fiber.Map{}
	closeAt := func(exec int64, at time.Time, state string) {
		if item, ok := open[exec]; ok {
			item["endDate"] = at
			item["state"] = state
			delete(open, exec)
		}
	}
	var last time.Time
	for _, row := range rows {
		last = row.OccurredAt
		if row.ExecutionID == nil || row.ElementID == nil {
			continue
		}
		exec, element := *row.ExecutionID, *row.ElementID
		switch models.AuditAction(row.Action) {
		case models.ActionExecutionCreated, models.ActionExecutionMoved, models.ActionExecutionWaited, models.ActionExecutionCompleted:
		default:
			continue
		}
		if item, ok := open[exec]; ok && item["elementId"] == element {
			continue
		}
		closeAt(exec, row.OccurredAt, "COMPLETED")
		item := fiber.Map{
			"elementInstanceKey":     uuid.NewSHA1(instanceID, []byte(strconv.Itoa(len(items)))),
			"processInstanceKey":     instanceID,
			"rootProcessInstanceKey": instanceID,
			"elementId":              element,
			"state":                  "ACTIVE",
			"startDate":              row.OccurredAt,
			"endDate":                nil,
			"hasIncident":            false,
			"incidentKey":            nil,
			"tenantId":               "<default>",
		}
		fc.enrichElementItem(item, instanceID, element)
		items = append(items, item)
		open[exec] = item
		if models.AuditAction(row.Action) == models.ActionExecutionCompleted {
			closeAt(exec, row.OccurredAt, "COMPLETED")
		}
	}
	for exec := range open {
		closeAt(exec, last, endState)
	}
	return items, nil
}
