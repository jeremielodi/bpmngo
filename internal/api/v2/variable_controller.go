package v2

import (
	"encoding/json"
	"sort"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jeremielodi/bpmngo/internal/repository"
	"github.com/jmoiron/sqlx"
)

type VariableController struct {
	db           *sqlx.DB
	instanceRepo *repository.ProcessInstanceRepository
}

func NewVariableController(db *sqlx.DB) *VariableController {
	return &VariableController{db: db, instanceRepo: repository.NewProcessInstanceRepository(db)}
}

// SearchVariablesRequest matches the (simplified) Camunda 8 search body.
type SearchVariablesRequest struct {
	Filter struct {
		ProcessInstanceKey string `json:"processInstanceKey"`
		Name               string `json:"name"`
	} `json:"filter"`
}

// SearchVariables handles POST /v2/variables/search. BpmnGo stores all of a
// process instance's variables as one JSONB blob rather than one row per
// variable, so this flattens that blob into individual name/value entries
// to match the Camunda 8 response shape.
func (vc *VariableController) SearchVariables(c *fiber.Ctx) error {
	var req SearchVariablesRequest
	_ = c.BodyParser(&req)

	if req.Filter.ProcessInstanceKey == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"title": "INVALID_ARGUMENT", "detail": "filter.processInstanceKey is required",
		})
	}
	instanceID, err := resolveInstanceKey(vc.db, req.Filter.ProcessInstanceKey)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"title": "INVALID_ARGUMENT", "detail": "invalid processInstanceKey",
		})
	}

	if !newInstanceAccess(c, vc.db, "VIEW").allowed(instanceID) {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"title": "NOT_FOUND", "detail": "process instance not found",
		})
	}

	vars, err := vc.instanceRepo.GetVariables(instanceID)
	if err == nil && len(vars) == 0 {
		// An archived instance: its variables moved to history.
		vars, err = repository.NewHistoricArchiveRepository(vc.db).FindVariablesByProcessInstance(instanceID)
	}
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"title": "INTERNAL_ERROR", "detail": err.Error()})
	}

	items := make([]fiber.Map, 0, len(vars))
	names := make([]string, 0, len(vars))
	for name := range vars {
		names = append(names, name)
	}
	sort.Strings(names)
	tenantID := "<default>"
	inst, _ := vc.instanceRepo.FindByID(instanceID)
	if inst == nil {
		inst, _ = vc.instanceRepo.FindHistoricByID(instanceID)
	}
	if inst != nil && inst.TenantID != nil && *inst.TenantID != "" {
		tenantID = *inst.TenantID
	}
	for _, name := range names {
		if req.Filter.Name != "" && name != req.Filter.Name {
			continue
		}
		encoded, _ := json.Marshal(vars[name])
		items = append(items, fiber.Map{
			"name":                   name,
			"value":                  string(encoded),
			"processInstanceKey":     instanceID,
			"rootProcessInstanceKey": instanceID,
			"scopeKey":               instanceID,
			"variableKey":            uuid.NewSHA1(instanceID, []byte("variable:"+name)),
			"tenantId":               tenantID,
			"isTruncated":            false,
		})
	}

	return c.JSON(fiber.Map{"items": items, "page": searchPage(items, "variableKey")})
}
