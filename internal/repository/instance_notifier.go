package repository

import (
	"sync"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// InstanceFinishedChannel is the Postgres NOTIFY channel announcing that a
// process instance was completed or terminated, so a caller waiting on it
// (gRPC CreateProcessInstanceWithResult) wakes on whichever BpmnGo instance
// it is connected to, not only the one that finished the instance.
const InstanceFinishedChannel = "goflow_instance_finished"

var (
	instanceWaitMu sync.Mutex
	instanceWaits  = make(map[uuid.UUID]chan struct{})
)

// NotifyInstanceFinished broadcasts that instanceID was completed or
// terminated. Must only be called once that status is committed.
func NotifyInstanceFinished(db *sqlx.DB, instanceID uuid.UUID) error {
	wakeInstance(instanceID)
	return NotifyInstanceFinishedTx(db, instanceID)
}

// NotifyInstanceFinishedTx queues the cross-instance notification in the
// transaction that changes the process status. PostgreSQL publishes it only
// if and after that transaction commits.
func NotifyInstanceFinishedTx(tx sqlx.Execer, instanceID uuid.UUID) error {
	_, err := tx.Exec(`SELECT pg_notify($1, $2)`, InstanceFinishedChannel, instanceID.String())
	return err
}

// WakeInstanceFinishedLocal wakes waiters in this process after the status
// transaction commits. The LISTEN notification also calls this function; a
// duplicate wake is harmless.
func WakeInstanceFinishedLocal(instanceID uuid.UUID) { wakeInstance(instanceID) }

// InstanceFinishedChannelFor returns a channel closed once instanceID is
// announced finished. Grab it *before* checking the instance's status, so
// an announcement racing with that check is never missed, and call the
// returned release when done waiting.
func InstanceFinishedChannelFor(instanceID uuid.UUID) (<-chan struct{}, func()) {
	instanceWaitMu.Lock()
	defer instanceWaitMu.Unlock()
	ch, ok := instanceWaits[instanceID]
	if !ok {
		ch = make(chan struct{})
		instanceWaits[instanceID] = ch
	}
	return ch, func() {
		instanceWaitMu.Lock()
		defer instanceWaitMu.Unlock()
		if instanceWaits[instanceID] == ch {
			delete(instanceWaits, instanceID)
		}
	}
}

func wakeInstance(instanceID uuid.UUID) {
	instanceWaitMu.Lock()
	defer instanceWaitMu.Unlock()
	if ch, ok := instanceWaits[instanceID]; ok {
		close(ch)
		delete(instanceWaits, instanceID)
	}
}
