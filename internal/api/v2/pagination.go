package v2

import (
	"encoding/base64"
	"encoding/json"

	"github.com/gofiber/fiber/v2"
)

// searchPage returns Camunda's search page shape. Cursors are deliberately
// opaque to clients and encode the first/last resource key as a JSON tuple,
// matching the format used by Camunda's REST API.
func searchPage(items []fiber.Map, keyField string) fiber.Map {
	page := fiber.Map{"totalItems": len(items), "hasMoreTotalItems": false}
	if len(items) == 0 {
		return page
	}
	page["startCursor"] = encodeSearchCursor(items[0][keyField])
	page["endCursor"] = encodeSearchCursor(items[len(items)-1][keyField])
	return page
}

func encodeSearchCursor(key interface{}) string {
	raw, _ := json.Marshal([]interface{}{key})
	return base64.StdEncoding.EncodeToString(raw)
}
