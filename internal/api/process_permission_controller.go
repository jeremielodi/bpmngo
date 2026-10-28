// internal/api/process_permission_controller.go
package api

import (
	"errors"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jeremielodi/bpmngo/internal/repository"
	"github.com/jeremielodi/bpmngo/pkg/common"
	"github.com/jmoiron/sqlx"
)

type ProcessPermissionController struct {
	db       *sqlx.DB
	permRepo *repository.ProcessPermissionRepository
}

func NewProcessPermissionController(db *sqlx.DB) *ProcessPermissionController {
	return &ProcessPermissionController{
		db:       db,
		permRepo: repository.NewProcessPermissionRepository(db),
	}
}

// requireManage answers 401/403 unless the caller may view/change
// processKey's own ACL, and returns false once it has written that
// response. That takes an explicit MANAGE grant or the global
// CAN_MANAGE_DEPLOY_PROCESS action. A process with no grants yet is open
// to everyone for VIEW/START/MANAGE, but not for its ACL: otherwise any
// user could grant themselves MANAGE and lock everyone else out.
func (pc *ProcessPermissionController) requireManage(c *fiber.Ctx, processKey string) bool {
	userID, err := uuid.Parse(common.GetUserUuid(c))
	if err != nil {
		c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"message": "unauthorized"})
		return false
	}
	allowed, err := pc.permRepo.UserHasProcessPermission(userID, processKey, "MANAGE")
	if err == nil && !allowed {
		allowed, err = repository.NewActionRepository(pc.db).UserHasPermission(userID, "CAN_MANAGE_DEPLOY_PROCESS")
	}
	if err != nil {
		c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"message": err.Error()})
		return false
	}
	if !allowed {
		c.Status(fiber.StatusForbidden).JSON(fiber.Map{"message": "you do not have MANAGE access to this process"})
		return false
	}
	return true
}

// GET /engine-rest/process-definition/key/:key/permissions
func (pc *ProcessPermissionController) ListPermissions(c *fiber.Ctx) error {
	key := c.Params("key")
	if !pc.requireManage(c, key) {
		return nil
	}

	perms, err := pc.permRepo.ListByProcessKey(key)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"message": err.Error()})
	}
	return c.JSON(fiber.Map{"permissions": perms})
}

// POST /engine-rest/process-definition/key/:key/permissions
func (pc *ProcessPermissionController) GrantPermission(c *fiber.Ctx) error {
	key := c.Params("key")
	if !pc.requireManage(c, key) {
		return nil
	}

	var req struct {
		GranteeType string `json:"granteeType"`
		GranteeID   string `json:"granteeId"`
		Permission  string `json:"permission"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"message": "invalid request body"})
	}
	if req.GranteeType != "user" && req.GranteeType != "role" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"message": "granteeType must be 'user' or 'role'"})
	}
	if req.Permission != "VIEW" && req.Permission != "START" && req.Permission != "MANAGE" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"message": "permission must be VIEW, START, or MANAGE"})
	}
	granteeID, err := uuid.Parse(req.GranteeID)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"message": "invalid granteeId"})
	}

	perm, err := pc.permRepo.Create(key, req.GranteeType, granteeID, req.Permission)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"message": err.Error()})
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"permission": perm})
}

// DELETE /engine-rest/process-definition/key/:key/permissions/:id
func (pc *ProcessPermissionController) RevokePermission(c *fiber.Ctx) error {
	key := c.Params("key")
	if !pc.requireManage(c, key) {
		return nil
	}

	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"message": "invalid permission id"})
	}
	deleted, err := pc.permRepo.DeleteUnlessLast(key, id)
	if errors.Is(err, repository.ErrLastPermission) {
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"message": err.Error()})
	}
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"message": err.Error()})
	}
	if !deleted {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"message": "no such permission on this process"})
	}
	return c.JSON(fiber.Map{"message": "permission revoked"})
}
