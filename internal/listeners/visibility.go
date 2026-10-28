package listeners

import (
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jeremielodi/bpmngo/internal/events"
	"github.com/jeremielodi/bpmngo/internal/service"
	"github.com/jeremielodi/bpmngo/pkg/common"
	"github.com/jmoiron/sqlx"
)

// Viewer is who a stream client is: task events reach it only for the
// instances it could read through the API (its tenant, VIEW grant).
type Viewer struct {
	UserID uuid.UUID
	Tenant string // "" = no tenant restriction
}

// viewerOf is the authenticated caller of a stream request.
func viewerOf(c *fiber.Ctx) Viewer {
	id, _ := uuid.Parse(common.GetUserUuid(c))
	return Viewer{UserID: id, Tenant: common.GetTenantId(c)}
}

// ViewerLocal is the fiber.Ctx local holding the Viewer of a WebSocket
// request, set before the upgrade (the session is not reachable after).
const ViewerLocal = "streamViewer"

// RememberViewer stores the caller as ViewerLocal, for HandleWebSocket.
func RememberViewer(c *fiber.Ctx) {
	c.Locals(ViewerLocal, viewerOf(c))
}

// visibility answers, for one event, which viewers may receive it. It
// looks the instance's tenant up once per event and each user's grant once
// per (event, user).
type visibility struct {
	db       *sqlx.DB
	event    *events.TaskEvent
	tenant   *string
	resolved bool
	byUser   map[uuid.UUID]bool
}

func newVisibility(db *sqlx.DB, event *events.TaskEvent) *visibility {
	return &visibility{db: db, event: event, byUser: map[uuid.UUID]bool{}}
}

func (v *visibility) allows(viewer Viewer) bool {
	if v.db == nil {
		return true // no database (unit tests of the filters only)
	}
	if viewer.UserID == uuid.Nil {
		return false
	}
	if viewer.Tenant != "" {
		if !v.resolved {
			v.resolved = true
			if id, err := uuid.Parse(v.event.ProcessInstanceID); err == nil {
				_ = v.db.Get(&v.tenant, `SELECT tenant_id FROM public.process_instances WHERE id = $1`, id)
			}
		}
		if v.tenant == nil || *v.tenant != viewer.Tenant {
			return false
		}
	}
	allowed, seen := v.byUser[viewer.UserID]
	if !seen {
		var ok bool
		var err error
		if v.event.ProcessKey != "" {
			ok, err = service.CanAccessProcess(v.db, viewer.UserID, v.event.ProcessKey, "VIEW")
		} else if id, perr := uuid.Parse(v.event.ProcessInstanceID); perr == nil {
			ok, err = service.CanAccessProcessInstance(v.db, viewer.UserID, id, "VIEW")
		}
		allowed = err == nil && ok
		v.byUser[viewer.UserID] = allowed
	}
	return allowed
}
