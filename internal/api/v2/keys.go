package v2

import (
	"bytes"
	"encoding/json"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jeremielodi/bpmngo/internal/repository"
	"github.com/jmoiron/sqlx"
)

// Keys in the /v2 API. BpmnGo's objects are identified by UUIDs, the gRPC
// gateway's by int64 keys (Zeebe's protocol), issued on first use in
// zeebe_keys. /v2 accepts both forms of a key, and answers with the UUID —
// or, with REST_KEYS=int, with the int64 key, as a string as Camunda does,
// so a client sees one key whichever API it calls.

// RestIntKeys tells whether /v2 answers with int64 keys (REST_KEYS=int).
func RestIntKeys() bool {
	restKeysOnce.Do(func() {
		switch m := strings.ToLower(strings.TrimSpace(os.Getenv("REST_KEYS"))); m {
		case "int":
			restKeysInt = true
		case "", "uuid":
		default:
			log.Printf("⚠️ unknown REST_KEYS %q — using \"uuid\"", m)
		}
	})
	return restKeysInt
}

var (
	restKeysOnce sync.Once
	restKeysInt  bool
)

// keyFields are the answer fields holding a key, with the zeebe_keys
// resource type of each (the gRPC gateway's types, so both APIs show the
// same key).
var keyFields = map[string]string{
	"processInstanceKey":       "process_instance",
	"parentProcessInstanceKey": "process_instance",
	"processDefinitionKey":     "process_definition",
	"jobKey":                   "job",
	"elementInstanceKey":       "element_instance",
	"flowNodeInstanceKey":      "element_instance",
	"incidentKey":              "incident",
	"userTaskKey":              "user_task",
	"jobId":                    "job", // POST /v2/jobs/:key/completion, when the instance ended
}

// keyListFields are the answer fields holding a list of keys.
var keyListFields = map[string]string{
	"correlatedProcessInstanceKeys": "process_instance",
	"startedProcessInstanceKeys":    "process_instance",
}

// routeKeyFields are keys whose field name only says so in one answer:
// given a field's path from the answer's root, its resource type ("" when
// it's no key).
var routeKeyFields = map[string]func(at []string) string{
	"/v2/messages/publication": func(at []string) string {
		if len(at) == 1 && at[0] == "key" {
			return "message"
		}
		return ""
	},
	// {"id": deployment, "deployedProcessDefinitions": {"<process id>": {"id": definition}}}
	"/v2/resources/deployments": func(at []string) string {
		switch {
		case len(at) == 1 && at[0] == "id":
			return "deployment"
		case len(at) == 3 && at[0] == "deployedProcessDefinitions" && at[2] == "id":
			return "process_definition"
		}
		return ""
	},
}

// pathKeys are the path segments followed by a key, with its resource type.
var pathKeys = map[string]string{
	"process-instances": "process_instance",
	"jobs":              "job",
	"user-tasks":        "user_task",
	"incidents":         "incident",
}

// KeyTranslation, mounted on /v2 after authentication, lets the handlers
// work with UUIDs only: an int64 key in the path is replaced by its UUID
// before routing, and with REST_KEYS=int the keys of a JSON answer are
// replaced by their int64 keys.
func KeyTranslation(db *sqlx.DB) fiber.Handler {
	keys := repository.NewZeebeKeyRepository(db)
	return func(c *fiber.Ctx) error {
		if path, changed := uuidPath(keys, c.Path()); changed {
			c.Path(path) // the routes below match the rewritten path
		}
		if err := c.Next(); err != nil || !RestIntKeys() {
			return err
		}
		if !strings.HasPrefix(string(c.Response().Header.ContentType()), fiber.MIMEApplicationJSON) {
			return nil
		}
		body, err := intKeysInJSON(keys, c.Path(), c.Response().Body())
		if err != nil {
			log.Printf("REST_KEYS=int: %s %s: %v", c.Method(), c.OriginalURL(), err)
			return nil // the answer goes out with its UUIDs
		}
		c.Response().SetBodyRaw(body)
		return nil
	}
}

// uuidPath replaces the int64 key after a known segment of path by its
// UUID. A key nothing was issued for becomes uuid.Nil: the handler then
// finds nothing, as for an unknown UUID.
func uuidPath(keys *repository.ZeebeKeyRepository, path string) (string, bool) {
	parts := strings.Split(path, "/")
	changed := false
	for i := 0; i+1 < len(parts); i++ {
		resourceType, ok := pathKeys[parts[i]]
		if !ok {
			continue
		}
		n, err := strconv.ParseInt(parts[i+1], 10, 64)
		if err != nil {
			continue // a UUID, or "search"
		}
		id, err := keys.ResolveKey(resourceType, n)
		if err != nil {
			id = uuid.Nil
		}
		parts[i+1] = id.String()
		changed = true
	}
	return strings.Join(parts, "/"), changed
}

// intKeysInJSON replaces, in a JSON answer of route, every key holding a
// UUID by its int64 key, as a string. Variables are data, never rewritten.
func intKeysInJSON(keys *repository.ZeebeKeyRepository, route string, body []byte) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber() // numbers in variables come back unchanged
	var doc interface{}
	if err := dec.Decode(&doc); err != nil {
		return nil, err
	}

	type slot struct {
		ref repository.KeyRef
		set func(key string)
	}
	var slots []slot
	add := func(resourceType string, val interface{}, set func(string)) {
		if s, ok := val.(string); ok {
			if id, err := uuid.Parse(s); err == nil {
				slots = append(slots, slot{repository.KeyRef{ResourceType: resourceType, ResourceID: id}, set})
			}
		}
	}
	routeFields := routeKeyFields[route]
	var walk func(v interface{}, at []string)
	walk = func(v interface{}, at []string) {
		switch x := v.(type) {
		case map[string]interface{}:
			for field, val := range x {
				obj, field := x, field
				here := append(at[:len(at):len(at)], field)
				resourceType, isKey := keyFields[field]
				if routeFields != nil {
					if t := routeFields(here); t != "" {
						resourceType, isKey = t, true
					}
				}
				if isKey {
					add(resourceType, val, func(k string) { obj[field] = k })
					continue
				}
				if resourceType, isList := keyListFields[field]; isList {
					if list, ok := val.([]interface{}); ok {
						for i := range list {
							i := i
							add(resourceType, list[i], func(k string) { list[i] = k })
						}
					}
					continue
				}
				if field == "variables" || field == "value" || field == "customHeaders" {
					continue
				}
				walk(val, here)
			}
		case []interface{}:
			for _, e := range x {
				walk(e, at)
			}
		}
	}
	walk(doc, nil)
	if len(slots) == 0 {
		return body, nil
	}

	refs := make([]repository.KeyRef, len(slots))
	for i, s := range slots {
		refs[i] = s.ref
	}
	issued, err := keys.GetOrAssignKeys(refs)
	if err != nil {
		return nil, err
	}
	for _, s := range slots {
		s.set(strconv.FormatInt(issued[s.ref], 10))
	}
	return json.Marshal(doc)
}
