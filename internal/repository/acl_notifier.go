package repository

import (
	"sync"

	"github.com/jmoiron/sqlx"
)

// ACLChangedChannel is the Postgres NOTIFY channel announcing that the
// grants of a process key changed. service.CanAccessProcess caches its
// decisions for a few seconds in each BpmnGo instance: every instance drops
// those of the key, not only the one that wrote the grant, so a revoked
// grant stops working everywhere at once. A notification missed while the
// LISTEN connection reconnects leaves the cache's expiry as the bound.
const ACLChangedChannel = "goflow_acl_changed"

var (
	aclChangedMu sync.RWMutex
	aclChanged   func(processKey string)
)

// OnACLChanged registers what a received ACLChangedChannel notification
// runs (the service's cache invalidation).
func OnACLChanged(f func(processKey string)) {
	aclChangedMu.Lock()
	defer aclChangedMu.Unlock()
	aclChanged = f
}

func aclChangedLocal(processKey string) {
	aclChangedMu.RLock()
	f := aclChanged
	aclChangedMu.RUnlock()
	if f != nil {
		f(processKey)
	}
}

// notifyACLChanged queues the notification in q's transaction: Postgres
// publishes it once that transaction commits.
func notifyACLChanged(q sqlx.Execer, processKey string) error {
	_, err := q.Exec(`SELECT pg_notify($1, $2)`, ACLChangedChannel, processKey)
	return err
}
