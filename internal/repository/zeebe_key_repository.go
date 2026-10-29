// internal/repository/zeebe_key_repository.go
package repository

import (
	"database/sql"
	"errors"
	"fmt"
	"sync"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

type ZeebeKeyRepository struct {
	db *sqlx.DB
}

func NewZeebeKeyRepository(db *sqlx.DB) *ZeebeKeyRepository {
	return &ZeebeKeyRepository{db: db}
}

// A resource's key never changes once issued (zeebe_keys rows are never
// updated or deleted), so every BpmnGo instance can remember it: the gRPC
// gateway and /v2 translate several keys per instance, and without this
// each translation was a query — the busiest one under load.
var keyCache = struct {
	sync.RWMutex
	byRef map[KeyRef]int64
	byKey map[keyOfType]uuid.UUID
}{byRef: map[KeyRef]int64{}, byKey: map[keyOfType]uuid.UUID{}}

type keyOfType struct {
	resourceType string
	key          int64
}

// Resources migrated by 009 own their protocol key. zeebe_keys remains a
// compatibility registry for resource types that do not have an engine_key
// column (signals, for example).
var engineKeyTables = map[string]string{
	"deployment":         "deployments",
	"process_definition": "process_definitions",
	"process_instance":   "process_instances",
	"message":            "published_messages",
}

func directEngineKey(db sqlx.Queryer, resourceType string, resourceID uuid.UUID) (int64, bool, error) {
	table, migrated := engineKeyTables[resourceType]
	if !migrated {
		return 0, false, nil
	}
	var key int64
	err := sqlx.Get(db, &key, fmt.Sprintf(`SELECT engine_key FROM public.%s WHERE id = $1`, table), resourceID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	return key, err == nil, err
}

func directResourceID(db sqlx.Queryer, resourceType string, key int64) (uuid.UUID, bool, error) {
	table, migrated := engineKeyTables[resourceType]
	if !migrated {
		return uuid.Nil, false, nil
	}
	var id uuid.UUID
	err := sqlx.Get(db, &id, fmt.Sprintf(`SELECT id FROM public.%s WHERE engine_key = $1`, table), key)
	if errors.Is(err, sql.ErrNoRows) {
		return uuid.Nil, false, nil
	}
	return id, err == nil, err
}

// keyCacheMax bounds each map (a new instance brings new keys forever);
// when full it starts over, the database still having every key.
const keyCacheMax = 200_000

func cachedKey(ref KeyRef) (int64, bool) {
	keyCache.RLock()
	defer keyCache.RUnlock()
	k, ok := keyCache.byRef[ref]
	return k, ok
}

func cachedID(resourceType string, key int64) (uuid.UUID, bool) {
	keyCache.RLock()
	defer keyCache.RUnlock()
	id, ok := keyCache.byKey[keyOfType{resourceType, key}]
	return id, ok
}

func rememberKey(ref KeyRef, key int64) {
	keyCache.Lock()
	defer keyCache.Unlock()
	if len(keyCache.byRef) >= keyCacheMax {
		keyCache.byRef = map[KeyRef]int64{}
	}
	if len(keyCache.byKey) >= keyCacheMax {
		keyCache.byKey = map[keyOfType]uuid.UUID{}
	}
	keyCache.byRef[ref] = key
	keyCache.byKey[keyOfType{ref.ResourceType, key}] = ref.ResourceID
}

// GetOrAssignKey returns the int64 Zeebe-protocol key for (resourceType,
// resourceID), assigning a new one on first use. Race-safe: of concurrent
// first uses, one inserts, the others find its row.
func (r *ZeebeKeyRepository) GetOrAssignKey(resourceType string, resourceID uuid.UUID) (int64, error) {
	ref := KeyRef{resourceType, resourceID}
	if key, ok := cachedKey(ref); ok {
		return key, nil
	}
	if key, found, err := directEngineKey(r.db, resourceType, resourceID); err != nil {
		return 0, err
	} else if found {
		rememberKey(ref, key)
		return key, nil
	}
	var key int64
	err := r.db.Get(&key, `
		INSERT INTO public.zeebe_keys (resource_type, resource_id)
		VALUES ($1, $2)
		ON CONFLICT (resource_type, resource_id) DO NOTHING
		RETURNING key
	`, resourceType, resourceID)
	if errors.Is(err, sql.ErrNoRows) { // issued already
		err = r.db.Get(&key, `
			SELECT key FROM public.zeebe_keys WHERE resource_type = $1 AND resource_id = $2
		`, resourceType, resourceID)
	}
	if err != nil {
		return 0, err
	}
	rememberKey(ref, key)
	return key, nil
}

// KeyRef names one resource in GetOrAssignKeys.
type KeyRef struct {
	ResourceType string
	ResourceID   uuid.UUID
}

// GetOrAssignKeys is GetOrAssignKey for many resources at once (a search
// answer's rows): the keys not remembered yet take one query to read, and
// one more to issue those never issued.
func (r *ZeebeKeyRepository) GetOrAssignKeys(refs []KeyRef) (map[KeyRef]int64, error) {
	keys := make(map[KeyRef]int64, len(refs))
	var missing []KeyRef
	seen := make(map[KeyRef]bool, len(refs))
	for _, ref := range refs {
		if seen[ref] {
			continue
		}
		seen[ref] = true
		if key, ok := cachedKey(ref); ok {
			keys[ref] = key
		} else if key, found, err := directEngineKey(r.db, ref.ResourceType, ref.ResourceID); err != nil {
			return nil, err
		} else if found {
			keys[ref] = key
			rememberKey(ref, key)
		} else {
			missing = append(missing, ref)
		}
	}
	if len(missing) == 0 {
		return keys, nil
	}
	type row struct {
		Key          int64     `db:"key"`
		ResourceType string    `db:"resource_type"`
		ResourceID   uuid.UUID `db:"resource_id"`
	}
	arrays := func(refs []KeyRef) (interface{}, interface{}) {
		types := make([]string, len(refs))
		ids := make([]string, len(refs))
		for i, ref := range refs {
			types[i], ids[i] = ref.ResourceType, ref.ResourceID.String()
		}
		return pq.Array(types), pq.Array(ids)
	}
	collect := func(rows []row) {
		for _, f := range rows {
			ref := KeyRef{f.ResourceType, f.ResourceID}
			keys[ref] = f.Key
			rememberKey(ref, f.Key)
		}
	}

	types, ids := arrays(missing)
	var found []row
	if err := r.db.Select(&found, `
		SELECT z.key, z.resource_type, z.resource_id
		FROM public.zeebe_keys z
		JOIN unnest($1::text[], $2::uuid[]) AS u(resource_type, resource_id)
		  ON z.resource_type = u.resource_type AND z.resource_id = u.resource_id
	`, types, ids); err != nil {
		return nil, err
	}
	collect(found)

	var unissued []KeyRef
	for _, ref := range missing {
		if _, ok := keys[ref]; !ok {
			unissued = append(unissued, ref)
		}
	}
	if len(unissued) == 0 {
		return keys, nil
	}
	types, ids = arrays(unissued)
	var issued []row
	if err := r.db.Select(&issued, `
		INSERT INTO public.zeebe_keys (resource_type, resource_id)
		SELECT resource_type, resource_id FROM unnest($1::text[], $2::uuid[]) AS u(resource_type, resource_id)
		ON CONFLICT (resource_type, resource_id)
		DO UPDATE SET resource_type = EXCLUDED.resource_type
		RETURNING key, resource_type, resource_id
	`, types, ids); err != nil {
		return nil, err
	}
	collect(issued)
	return keys, nil
}

// ResolveKey reverses GetOrAssignKey — looks up the UUID a previously
// issued int64 key refers to, scoped to resourceType.
func (r *ZeebeKeyRepository) ResolveKey(resourceType string, key int64) (uuid.UUID, error) {
	if id, ok := cachedID(resourceType, key); ok {
		return id, nil
	}
	if id, found, err := directResourceID(r.db, resourceType, key); err != nil {
		return uuid.Nil, err
	} else if found {
		rememberKey(KeyRef{resourceType, id}, key)
		return id, nil
	}
	var resourceID uuid.UUID
	err := r.db.Get(&resourceID, `
		SELECT resource_id FROM public.zeebe_keys
		WHERE resource_type = $1 AND key = $2
	`, resourceType, key)
	if err == nil {
		rememberKey(KeyRef{resourceType, resourceID}, key)
	}
	return resourceID, err
}
