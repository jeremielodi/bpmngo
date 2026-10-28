// internal/repository/process_permission_repository.go
package repository

import (
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// ProcessPermission is a single ACL grant on a process_key. When a
// process_key has zero ProcessPermission rows it is unrestricted — open
// to any authenticated user (see ProcessAuthorizationService).
type ProcessPermission struct {
	ID           uuid.UUID `db:"id" json:"id"`
	ResourceType string    `db:"resource_type" json:"resourceType"`
	ProcessKey   string    `db:"process_key" json:"processKey"`
	GranteeType  string    `db:"grantee_type" json:"granteeType"`
	GranteeID    uuid.UUID `db:"grantee_id" json:"granteeId"`
	Permission   string    `db:"permission" json:"permission"`
	CreatedAt    time.Time `db:"created_at" json:"createdAt"`
}

// permissionRank orders VIEW < START < MANAGE so a higher grant
// satisfies a check for a lower level (MANAGE implies START implies VIEW).
var permissionRank = map[string]int{"VIEW": 1, "START": 2, "MANAGE": 3}

type ProcessPermissionRepository struct {
	db *sqlx.DB
}

func NewProcessPermissionRepository(db *sqlx.DB) *ProcessPermissionRepository {
	return &ProcessPermissionRepository{db: db}
}

func (r *ProcessPermissionRepository) ListByProcessKey(processKey string) ([]ProcessPermission, error) {
	perms := []ProcessPermission{}
	err := r.db.Select(&perms, `
		SELECT id, resource_type, process_key, grantee_type, grantee_id, permission, created_at
		FROM public.process_permissions
		WHERE process_key = $1
		ORDER BY created_at
	`, processKey)
	return perms, err
}

func (r *ProcessPermissionRepository) Create(processKey, granteeType string, granteeID uuid.UUID, permission string) (ProcessPermission, error) {
	perm := ProcessPermission{
		ID:           uuid.New(),
		ResourceType: "process_definition",
		ProcessKey:   processKey,
		GranteeType:  granteeType,
		GranteeID:    granteeID,
		Permission:   permission,
	}
	_, err := r.db.Exec(`
		INSERT INTO public.process_permissions (id, resource_type, process_key, grantee_type, grantee_id, permission)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, perm.ID, perm.ResourceType, perm.ProcessKey, perm.GranteeType, perm.GranteeID, perm.Permission)
	return perm, err
}

// ErrLastPermission: the grant is the process's last one. A process keeps
// at least one grant, so it never falls back to "open to every user".
var ErrLastPermission = errors.New("the last permission of a process cannot be revoked")

// DeleteUnlessLast is Delete, refused with ErrLastPermission when id is the
// last grant of processKey. The key's grants are locked meanwhile, so two
// concurrent revocations cannot both remove "one of the two".
func (r *ProcessPermissionRepository) DeleteUnlessLast(processKey string, id uuid.UUID) (bool, error) {
	tx, err := r.db.Beginx()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var ids []uuid.UUID
	if err := tx.Select(&ids, `SELECT id FROM public.process_permissions WHERE process_key = $1 FOR UPDATE`, processKey); err != nil {
		return false, err
	}
	found := false
	for _, g := range ids {
		found = found || g == id
	}
	if !found {
		return false, nil
	}
	if len(ids) == 1 {
		return false, ErrLastPermission
	}
	if _, err := tx.Exec(`DELETE FROM public.process_permissions WHERE id = $1 AND process_key = $2`, id, processKey); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// IsRestricted reports whether processKey has any ACL grants at all —
// if not, access is open to everyone (unchanged, backward-compatible
// behavior).
func (r *ProcessPermissionRepository) IsRestricted(processKey string) (bool, error) {
	var count int
	err := r.db.Get(&count, `SELECT COUNT(*) FROM public.process_permissions WHERE process_key = $1`, processKey)
	return count > 0, err
}

// UserHasProcessPermission reports whether userID has a grant on
// processKey — direct, or via any role the user holds — at or above
// level (MANAGE implies START implies VIEW).
func (r *ProcessPermissionRepository) UserHasProcessPermission(userID uuid.UUID, processKey, level string) (bool, error) {
	var grants []string
	err := r.db.Select(&grants, `
		SELECT permission FROM public.process_permissions
		WHERE process_key = $1 AND grantee_type = 'user' AND grantee_id = $2
		UNION
		SELECT pp.permission FROM public.process_permissions pp
		JOIN public.user_roles ur ON ur.roles_id = pp.grantee_id
		WHERE pp.process_key = $1 AND pp.grantee_type = 'role' AND ur.user_id = $2
	`, processKey, userID)
	if err != nil {
		return false, err
	}

	required := permissionRank[level]
	for _, g := range grants {
		if permissionRank[g] >= required {
			return true, nil
		}
	}
	return false, nil
}
