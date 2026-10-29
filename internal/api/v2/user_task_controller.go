package v2

import (
	"strconv"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jeremielodi/bpmngo/internal/events"
	"github.com/jeremielodi/bpmngo/internal/models"
	"github.com/jeremielodi/bpmngo/internal/repository"
	"github.com/jmoiron/sqlx"
)

type UserTaskController struct {
	db         *sqlx.DB
	dispatcher *events.TaskEventDispatcher
	taskRepo   *repository.TaskRepository
	engineRepo *repository.EngineRepository
}

func NewUserTaskController(db *sqlx.DB, dispatcher *events.TaskEventDispatcher) *UserTaskController {
	return &UserTaskController{
		db:         db,
		dispatcher: dispatcher,
		taskRepo:   repository.NewTaskRepository(db, dispatcher),
		engineRepo: repository.NewEngineRepository(db, dispatcher),
	}
}

// userTaskState maps BpmnGo's task status to the state names used by the
// Camunda 8 Tasklist API.
func userTaskState(status string) string {
	switch status {
	case "created":
		return "CREATED"
	case "claimed":
		return "ASSIGNED"
	case "completed":
		return "COMPLETED"
	case "cancelled":
		return "CANCELED"
	default:
		return status
	}
}

func (uc *UserTaskController) toV2UserTask(t *models.Task) fiber.Map {
	var formKey string
	var processDefinitionID interface{}
	processDefinitionKey := ""
	processDefinitionVersion := 0
	processName := interface{}(nil)
	tenantID := "<default>"
	if graph, err := uc.engineRepo.GetProcessGraphByInstanceID(t.ProcessInstanceID); err == nil && graph != nil {
		if n, ok := graph.Nodes[t.TaskDefinitionKey]; ok {
			formKey = n.FormKey
		}
	}
	if inst, err := repository.NewProcessInstanceRepository(uc.db).FindByID(t.ProcessInstanceID); err == nil && inst != nil {
		processDefinitionID = inst.ProcessDefinitionID
		processDefinitionKey = inst.ProcessKey
		processDefinitionVersion = inst.Version
		if inst.ProcessName != "" && inst.ProcessName != inst.ProcessKey {
			processName = inst.ProcessName
		}
		if inst.TenantID != nil && *inst.TenantID != "" {
			tenantID = *inst.TenantID
		}
	}
	candidateGroups := []string{}
	if t.CandidateGroup != nil && *t.CandidateGroup != "" {
		candidateGroups = append(candidateGroups, *t.CandidateGroup)
	}

	name := t.TaskName
	if name == "" {
		name = t.TaskDefinitionKey
	}
	var responseFormKey interface{}
	if formKey != "" {
		responseFormKey = formKey
	}

	return fiber.Map{
		"userTaskKey":              strconv.FormatInt(t.ID, 10),
		"elementInstanceKey":       int64String(t.ExecutionID),
		"elementId":                t.TaskDefinitionKey,
		"name":                     name,
		"processInstanceKey":       t.ProcessInstanceID,
		"assignee":                 t.Assignee,
		"candidateGroups":          candidateGroups,
		"candidateUsers":           []string{},
		"state":                    userTaskState(t.Status),
		"creationDate":             t.CreatedAt,
		"completionDate":           t.CompletedAt,
		"formKey":                  responseFormKey,
		"priority":                 t.Priority,
		"processDefinitionId":      processDefinitionKey,
		"processDefinitionKey":     processDefinitionID,
		"processDefinitionVersion": processDefinitionVersion,
		"processName":              processName,
		"rootProcessInstanceKey":   t.ProcessInstanceID,
		"tenantId":                 tenantID,
		"businessId":               nil,
		"customHeaders":            fiber.Map{},
		"dueDate":                  nil,
		"externalFormReference":    nil,
		"followUpDate":             nil,
		"tags":                     []string{},
	}
}

func int64String(v *int64) interface{} {
	if v == nil {
		return nil
	}
	return strconv.FormatInt(*v, 10)
}

// SearchUserTasksRequest matches the (simplified) Camunda 8 search body.
type SearchUserTasksRequest struct {
	Filter struct {
		ProcessInstanceKey string `json:"processInstanceKey"`
		Assignee           string `json:"assignee"`
		State              string `json:"state"`
		ElementId          string `json:"elementId"`
		CandidateGroup     string `json:"candidateGroup"`
		Priority           *int   `json:"priority"`
	} `json:"filter"`
}

// SearchUserTasks handles POST /v2/user-tasks/search.
func (uc *UserTaskController) SearchUserTasks(c *fiber.Ctx) error {
	var req SearchUserTasksRequest
	_ = c.BodyParser(&req)

	params := map[string]interface{}{}
	var instanceID uuid.UUID
	if req.Filter.ProcessInstanceKey != "" {
		var err error
		if instanceID, err = resolveInstanceKey(uc.db, req.Filter.ProcessInstanceKey); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"title": "INVALID_ARGUMENT", "detail": "invalid processInstanceKey",
			})
		}
		params["process_instance_id"] = instanceID.String()
	}
	if req.Filter.Assignee != "" {
		params["assignee"] = req.Filter.Assignee
	}
	if req.Filter.ElementId != "" {
		params["task_definition_key"] = req.Filter.ElementId
	}
	if req.Filter.CandidateGroup != "" {
		params["candidate_group"] = req.Filter.CandidateGroup
	}
	if req.Filter.Priority != nil {
		params["priority"] = *req.Filter.Priority
	}
	if req.Filter.State != "" {
		switch req.Filter.State {
		case "CREATED":
			params["status"] = "created"
		case "ASSIGNED":
			params["status"] = "claimed"
		case "COMPLETED":
			params["status"] = "completed"
		case "CANCELED":
			params["status"] = "cancelled"
		}
	}

	tasks, err := uc.taskRepo.FindAll(params)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"title": "INTERNAL_ERROR", "detail": err.Error()})
	}

	// Only the tasks of instances the caller may VIEW, as GET /engine-rest/tasks.
	access := newInstanceAccess(c, uc.db, "VIEW")
	items := make([]fiber.Map, 0, len(tasks))
	for i := range tasks {
		if !access.allowed(tasks[i].ProcessInstanceID) {
			continue
		}
		items = append(items, uc.toV2UserTask(&tasks[i]))
	}

	// The live `tasks` table only ever holds tasks of still-running
	// instances now — once an instance completes/terminates it's archived
	// (see archive_service.go) and its tasks move to historic_tasks. Merge
	// those in when a specific instance was searched, so a task search
	// right after that instance finishes still finds it (candidateGroup
	// isn't stored on historic_tasks, so skip the merge for that filter —
	// a known, narrow limitation rather than returning wrong results).
	if req.Filter.ProcessInstanceKey != "" && req.Filter.CandidateGroup == "" {
		if access.allowed(instanceID) {
			archiveRepo := repository.NewHistoricArchiveRepository(uc.db)
			historicTasks, histErr := archiveRepo.FindTasksByProcessInstance(instanceID)
			if histErr == nil {
				for _, ht := range historicTasks {
					if req.Filter.ElementId != "" && ht.TaskDefinitionKey != req.Filter.ElementId {
						continue
					}
					if req.Filter.Assignee != "" && (ht.Assignee == nil || *ht.Assignee != req.Filter.Assignee) {
						continue
					}
					if req.Filter.Priority != nil && (ht.Priority == nil || *ht.Priority != *req.Filter.Priority) {
						continue
					}
					if params["status"] != nil && ht.Status != params["status"] {
						continue
					}
					items = append(items, uc.toV2UserTask(&models.Task{
						ID:                ht.ID,
						ProcessInstanceID: ht.ProcessInstanceID,
						TaskDefinitionKey: ht.TaskDefinitionKey,
						TaskName:          derefOrEmpty(ht.TaskName),
						Assignee:          ht.Assignee,
						Status:            ht.Status,
						Priority:          derefIntOr(ht.Priority, 0),
						CreatedAt:         ht.CreatedAt,
						ClaimedAt:         ht.ClaimedAt,
						CompletedAt:       ht.CompletedAt,
					}))
				}
			}
		}
	}

	return c.JSON(fiber.Map{"items": items})
}

func derefOrEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func derefIntOr(i *int, def int) int {
	if i == nil {
		return def
	}
	return *i
}
