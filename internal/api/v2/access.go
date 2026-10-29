package v2

import (
	"database/sql"
	"errors"
	"fmt"
	"strconv"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jeremielodi/bpmngo/internal/repository"
	"github.com/jeremielodi/bpmngo/internal/service"
	"github.com/jeremielodi/bpmngo/pkg/common"
	"github.com/jmoiron/sqlx"
)

// instanceAccess answers whether the caller has `level` access to a
// process instance (service.CanAccessProcessInstance), remembering each
// answer for the request: a search's rows often share a few instances.
// Like the v1 controllers' checks, it fails open without an authenticated
// user, the route-level auth already covering that case.
type instanceAccess struct {
	db     *sqlx.DB
	userID uuid.UUID
	anon   bool
	level  string
	seen   map[uuid.UUID]bool
}

func newInstanceAccess(c *fiber.Ctx, db *sqlx.DB, level string) *instanceAccess {
	userID, err := uuid.Parse(common.GetUserUuid(c))
	return &instanceAccess{db: db, userID: userID, anon: err != nil, level: level, seen: map[uuid.UUID]bool{}}
}

// resolveInstanceKey reads a process instance key in either form a client
// holds: the UUID this REST API returns, or the int64 key the gRPC gateway
// returns (a client may start an instance over gRPC and finish its user
// task over REST, as against Camunda). An int64 key nothing was issued for
// resolves to uuid.Nil, which matches no instance: a search finds nothing
// and a lookup answers 404, not 400.
func resolveInstanceKey(db *sqlx.DB, key string) (uuid.UUID, error) {
	return resolveKey(db, "process_instance", key)
}

// resolveKey is resolveInstanceKey for any zeebe_keys resource type.
func resolveKey(db *sqlx.DB, resourceType, key string) (uuid.UUID, error) {
	if id, err := uuid.Parse(key); err == nil {
		return id, nil
	}
	n, err := strconv.ParseInt(key, 10, 64)
	if err != nil {
		return uuid.Nil, fmt.Errorf("invalid key %q", key)
	}
	id, err := repository.NewZeebeKeyRepository(db).ResolveKey(resourceType, n)
	if errors.Is(err, sql.ErrNoRows) {
		return uuid.Nil, nil
	}
	return id, err
}

func (a *instanceAccess) allowed(instanceID uuid.UUID) bool {
	if a.anon {
		return true
	}
	if ok, done := a.seen[instanceID]; done {
		return ok
	}
	ok, err := service.CanAccessProcessInstance(a.db, a.userID, instanceID, a.level)
	ok = ok && err == nil
	a.seen[instanceID] = ok
	return ok
}
