package v2

import (
	"encoding/base64"
	"testing"

	"github.com/gofiber/fiber/v2"
)

func TestSearchPageHasOpaqueBoundaryCursors(t *testing.T) {
	page := searchPage([]fiber.Map{{"key": "10"}, {"key": "20"}}, "key")
	if page["totalItems"] != 2 || page["hasMoreTotalItems"] != false {
		t.Fatalf("unexpected page: %#v", page)
	}
	start, err := base64.StdEncoding.DecodeString(page["startCursor"].(string))
	if err != nil || string(start) != `["10"]` {
		t.Fatalf("unexpected start cursor %q: %v", start, err)
	}
	end, err := base64.StdEncoding.DecodeString(page["endCursor"].(string))
	if err != nil || string(end) != `["20"]` {
		t.Fatalf("unexpected end cursor %q: %v", end, err)
	}
}
