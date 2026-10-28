// internal/service/default_data_service.go
package service

import (
	"database/sql"
	"fmt"
	"log"
	"strings"

	"github.com/google/uuid"
	"github.com/jeremielodi/bpmngo/internal/models"
	"github.com/jeremielodi/bpmngo/internal/repository"
	"github.com/jeremielodi/bpmngo/pkg/common"
	"github.com/jmoiron/sqlx"
	"github.com/joho/godotenv"
	"golang.org/x/crypto/bcrypt"
)

// DefaultDataConfig holds configuration for default data
type DefaultDataConfig struct {
	SuperUserName     string
	SuperUserEmail    string
	SuperUserPassword string
	// CreateSuperUser is false when CREATE_SUPER_USER=false: the super user
	// (and its super_admin role assignment) is left alone at startup.
	CreateSuperUser bool
}

// LoadDefaultDataConfig loads configuration from environment variables
func LoadDefaultDataConfig(util common.Util) DefaultDataConfig {
	// Load .env file if it exists (ignore error if not found)
	_ = godotenv.Load()

	return DefaultDataConfig{
		SuperUserName:     common.Util.DotEnvVariable(util, "SUPER_USER_NAME"),
		SuperUserEmail:    common.Util.DotEnvVariable(util, "SUPER_USER_EMAIL"),
		SuperUserPassword: common.Util.DotEnvVariable(util, "SUPER_USER_PASSWORD"),
		// Unset means true, so existing deployments keep seeding as before.
		CreateSuperUser: !strings.EqualFold(strings.TrimSpace(common.Util.DotEnvVariable(util, "CREATE_SUPER_USER")), "false"),
	}
}

// CreateDefaultData creates default roles and users if they don't exist
func CreateDefaultData(db *sqlx.DB, util common.Util) error {
	config := LoadDefaultDataConfig(util)

	// Begin transaction
	tx, err := db.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	roleRepo := repository.NewRoleRepository(db)
	userRepo := repository.NewUserRepository(db)

	// =========================================================
	// 1. CREATE ROLES (if they don't exist)
	// =========================================================

	roles := []struct {
		Role         models.Role
		SkipIfExists bool
	}{
		{
			Role: models.Role{
				ID:        uuid.New(),
				Label:     "super_admin",
				IsDefault: 0,
			},
			SkipIfExists: true,
		},
		{
			Role: models.Role{
				ID:        uuid.New(),
				Label:     "admin",
				IsDefault: 0,
			},
			SkipIfExists: true,
		},
		{
			Role: models.Role{
				ID:        uuid.New(),
				Label:     "user",
				IsDefault: 1,
			},
			SkipIfExists: true,
		},
	}

	for _, r := range roles {
		// Check if role already exists
		var exists bool
		checkQuery := `SELECT EXISTS(SELECT 1 FROM public.roles WHERE label = $1)`
		err := tx.QueryRow(checkQuery, r.Role.Label).Scan(&exists)
		if err != nil {
			return err
		}

		if exists {
			log.Printf("⏭️ Role '%s' already exists, skipping creation", r.Role.Label)
			continue
		}

		if err := roleRepo.CreateWithTx(tx, r.Role); err != nil {
			return err
		}
		log.Printf("✅ Role '%s' created successfully", r.Role.Label)
	}

	// =========================================================
	// 2. CREATE SUPER USER (if doesn't exist)
	// =========================================================

	var superUserID uuid.UUID

	// Check if super user already exists
	var userExists bool
	if config.CreateSuperUser {
		checkUserQuery := `SELECT EXISTS(SELECT 1 FROM public.users WHERE email = $1)`
		if err := tx.QueryRow(checkUserQuery, config.SuperUserEmail).Scan(&userExists); err != nil {
			return err
		}
	}

	if !config.CreateSuperUser {
		log.Printf("⏭️ CREATE_SUPER_USER=false, skipping super user creation")
	} else if userExists {
		log.Printf("⏭️ User '%s' already exists, skipping creation", config.SuperUserEmail)

		// Get existing user ID
		var existingUser models.User
		err = tx.Get(&existingUser, `SELECT id, email FROM public.users WHERE email = $1`, config.SuperUserEmail)
		if err != nil {
			return err
		}
		superUserID = existingUser.ID
	} else {
		if config.SuperUserPassword == "" {
			return fmt.Errorf("SUPER_USER_PASSWORD is not set — refusing to create super user '%s' with an empty password", config.SuperUserEmail)
		}
		if config.SuperUserPassword == "admin123" || config.SuperUserPassword == "change_me" {
			log.Printf("⚠️ Super user '%s' is being created with a sample password — change it right after first login", config.SuperUserEmail)
		}

		// Create super user with bcrypt hash
		passwordHash, err := bcrypt.GenerateFromPassword([]byte(config.SuperUserPassword), bcrypt.DefaultCost)
		if err != nil {
			return err
		}

		superUser := models.UserCreateModel{
			ID:           uuid.New(),
			Email:        config.SuperUserEmail,
			FullName:     config.SuperUserName,
			PasswordHash: string(passwordHash),
			IsActive:     true,
		}

		if _, err := userRepo.CreateWithTx(tx, superUser); err != nil {
			return err
		}
		superUserID = superUser.ID
		log.Printf("✅ Super user '%s' created successfully", config.SuperUserEmail)
	}

	// =========================================================
	// 3. ASSIGN SUPER_ADMIN ROLE TO SUPER USER (if not already assigned)
	// =========================================================

	// Get super_admin role ID
	var superAdminRoleID uuid.UUID
	err = tx.Get(&superAdminRoleID, `SELECT id FROM public.roles WHERE label = 'super_admin'`)
	if err != nil {
		if err == sql.ErrNoRows {
			log.Printf("⚠️ Super_admin role not found, skipping assignment")
		} else {
			return err
		}
	}

	if superAdminRoleID != uuid.Nil && superUserID != uuid.Nil {
		// Check if role is already assigned
		var roleAssigned bool
		checkAssignmentQuery := `
			SELECT EXISTS(
				SELECT 1 FROM public.user_roles 
				WHERE user_id = $1 AND roles_id = $2
			)
		`
		err = tx.QueryRow(checkAssignmentQuery, superUserID, superAdminRoleID).Scan(&roleAssigned)
		if err != nil {
			return err
		}

		if !roleAssigned {
			if err := roleRepo.AssignRoleToUserWithTx(tx, superUserID, superAdminRoleID); err != nil {
				return err
			}
			log.Printf("✅ Super_admin role assigned to user '%s'", config.SuperUserEmail)
		} else {
			log.Printf("⏭️ Super_admin role already assigned to user '%s'", config.SuperUserEmail)
		}
	}

	// =========================================================
	// 4. ASSIGN ALL ACTIONS TO SUPER_ADMIN ROLE (if not already assigned)
	// =========================================================

	if superAdminRoleID != uuid.Nil {
		// Get all action IDs
		var actionIDs []int
		err = tx.Select(&actionIDs, `SELECT id FROM public.actions`)
		if err != nil {
			return err
		}

		assignedCount := 0
		skippedCount := 0

		for _, actionID := range actionIDs {
			// Check if action is already assigned to role
			var actionAssigned bool
			checkActionQuery := `
				SELECT EXISTS(
					SELECT 1 FROM public.role_actions 
					WHERE roles_id = $1 AND action_id = $2
				)
			`
			err = tx.QueryRow(checkActionQuery, superAdminRoleID, actionID).Scan(&actionAssigned)
			if err != nil {
				return err
			}

			if !actionAssigned {
				_, err := tx.Exec(`
					INSERT INTO public.role_actions (uuid, roles_id, action_id)
					VALUES ($1, $2, $3)
				`, uuid.New(), superAdminRoleID, actionID)
				if err != nil {
					return err
				}
				assignedCount++
			} else {
				skippedCount++
			}
		}

		if assignedCount > 0 {
			log.Printf("✅ Assigned %d actions to super_admin role", assignedCount)
		}
		if skippedCount > 0 {
			log.Printf("⏭️ Skipped %d already assigned actions", skippedCount)
		}
	}

	// =========================================================
	// 6. COMMIT TRANSACTION
	// =========================================================

	if err := tx.Commit(); err != nil {
		return err
	}

	log.Println("📊 Default data migration summary:")
	log.Printf("   - Roles: super_admin, admin, user (created if missing)")
	log.Printf("   - Super user: %s (created if missing)", config.SuperUserEmail)
	log.Printf("   - All actions assigned to super_admin role")

	return nil
}
