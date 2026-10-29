package repository

import (
	"sync"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// Public UUIDs are immutable aliases of engine keys. Runtime records already
// load both values together, so remember that association instead of asking
// PostgreSQL to resolve the same UUID for every repository and audit write.
// The cache is deliberately bounded: it is a compatibility bridge for APIs
// that still accept UUIDs, not another source of truth.
const processInstanceKeyCacheMax = 131072

var processInstanceKeyCache = struct {
	sync.RWMutex
	values map[uuid.UUID]int64
}{values: make(map[uuid.UUID]int64)}

func RememberProcessInstanceKey(id uuid.UUID, key int64) {
	if id == uuid.Nil || key == 0 {
		return
	}
	processInstanceKeyCache.Lock()
	defer processInstanceKeyCache.Unlock()
	if len(processInstanceKeyCache.values) >= processInstanceKeyCacheMax {
		// Evict one arbitrary old association. Keys and UUID aliases never
		// change, so eviction affects only hit rate, never correctness.
		for victim := range processInstanceKeyCache.values {
			delete(processInstanceKeyCache.values, victim)
			break
		}
	}
	processInstanceKeyCache.values[id] = key
}

func ClearProcessInstanceKeyCache() {
	processInstanceKeyCache.Lock()
	processInstanceKeyCache.values = make(map[uuid.UUID]int64)
	processInstanceKeyCache.Unlock()
}

// ForgetProcessInstanceKey removes a speculative association only when it
// still has the expected value. Process creation uses this on rollback.
func ForgetProcessInstanceKey(id uuid.UUID, key int64) {
	processInstanceKeyCache.Lock()
	if processInstanceKeyCache.values[id] == key {
		delete(processInstanceKeyCache.values, id)
	}
	processInstanceKeyCache.Unlock()
}

func processInstanceEngineKey(q sqlx.Queryer, id uuid.UUID) (int64, error) {
	return ProcessInstanceEngineKey(q, id)
}

// ProcessInstanceEngineKey resolves a public UUID once, before a write. Citus
// does not support scalar subqueries inside INSERT statements; passing the
// BIGINT explicitly also keeps repositories honest about the physical FK.
func ProcessInstanceEngineKey(q sqlx.Queryer, id uuid.UUID) (int64, error) {
	processInstanceKeyCache.RLock()
	key, ok := processInstanceKeyCache.values[id]
	processInstanceKeyCache.RUnlock()
	if ok {
		return key, nil
	}
	err := sqlx.Get(q, &key, `SELECT engine_key FROM public.process_instances WHERE id = $1`, id)
	if err == nil {
		RememberProcessInstanceKey(id, key)
	}
	return key, err
}

func processDefinitionEngineKey(q sqlx.Queryer, id uuid.UUID) (int64, error) {
	return ProcessDefinitionEngineKey(q, id)
}

func ProcessDefinitionEngineKey(q sqlx.Queryer, id uuid.UUID) (int64, error) {
	var key int64
	err := sqlx.Get(q, &key, `SELECT engine_key FROM public.process_definitions WHERE id = $1`, id)
	return key, err
}

func PublishedMessageEngineKey(q sqlx.Queryer, id uuid.UUID) (int64, error) {
	var key int64
	err := sqlx.Get(q, &key, `SELECT engine_key FROM public.published_messages WHERE id = $1`, id)
	return key, err
}
