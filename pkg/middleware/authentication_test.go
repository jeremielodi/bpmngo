package middleware

import (
	"database/sql"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gofiber/fiber/v2"
	"github.com/golang-jwt/jwt/v4"
	"github.com/google/uuid"
	"github.com/jeremielodi/bpmngo/internal/models"
	"github.com/jeremielodi/bpmngo/pkg/authentication"
	"github.com/jeremielodi/bpmngo/pkg/common"
	"github.com/jmoiron/sqlx"
	"golang.org/x/crypto/bcrypt"
)

const testSecret = "test-secret-test-secret-test-secret"

func TestWithinPublicRoutes(t *testing.T) {
	cases := []struct {
		path   string
		public bool
	}{
		{"/", true},
		{"/auth/login", true},
		{"/auth/refresh", true},
		{"/health", true},
		{"/static/app.js", true},
		{"/app", true},
		{"/app/index.html", true},

		// A public entry must match a whole path segment, never a substring:
		// these all reach protected handlers.
		{"/application", false},
		{"/healthz", false},
		{"/auth/logout", false},
		{"/engine-rest/process-definition/key/approval/start", false},
		{"/engine-rest/process-definition/key/newsletter/start", false},
		{"/engine-rest/process-instance/x/variables/binder", false},
		{"/engine-rest/task/x/login", false},
		{"/users/me", false},
		{"/ws/tasks", false},
		{"/events/tasks", false},
	}
	for _, tc := range cases {
		if got := WithinPublicRoutes(tc.path, PublicRoutes); got != tc.public {
			t.Errorf("WithinPublicRoutes(%q) = %v, want %v", tc.path, got, tc.public)
		}
	}
}

// newAuthApp mounts CombinedAuthMiddleware in front of a handler that echoes
// the user id the middleware put in the session.
func newAuthApp(t *testing.T, db *sqlx.DB) *fiber.App {
	t.Helper()
	jwtService := authentication.NewJWTService(db, testSecret)
	app := fiber.New()
	app.Use(CombinedAuthMiddleware(db, jwtService, nil, common.Util{}))
	app.Get("/*", func(c *fiber.Ctx) error {
		return c.SendString(common.GetUserUuid(c))
	})
	return app
}

func do(t *testing.T, app *fiber.App, req *http.Request) (int, string) {
	t.Helper()
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

func accessToken(t *testing.T, secret string, userID string, notBefore, expires time.Time) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, authentication.CustomClaims{
		UserId:    userID,
		TokenType: "access",
		RegisteredClaims: jwt.RegisteredClaims{
			NotBefore: jwt.NewNumericDate(notBefore),
			ExpiresAt: jwt.NewNumericDate(expires),
			Issuer:    authentication.Issuer,
		},
	})
	signed, err := token.SignedString([]byte(secret))
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

func TestCombinedAuth_Bearer(t *testing.T) {
	app := newAuthApp(t, nil)
	userID := uuid.New().String()
	now := time.Now()

	valid, err := authentication.NewJWTService(nil, testSecret).CreateAccessToken(models.User{ID: uuid.MustParse(userID)})
	if err != nil {
		t.Fatal(err)
	}
	expired := accessToken(t, testSecret, userID, now.Add(-time.Hour), now.Add(-time.Minute))
	otherKey := accessToken(t, "another-secret-another-secret-xx", userID, now.Add(-time.Minute), now.Add(time.Hour))

	cases := []struct {
		name   string
		setup  func(r *http.Request)
		status int
	}{
		{"no credentials", func(r *http.Request) {}, http.StatusUnauthorized},
		{"valid bearer", func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+valid) }, http.StatusOK},
		{"valid X-Access-Token", func(r *http.Request) { r.Header.Set("X-Access-Token", valid) }, http.StatusOK},
		{"valid cookie", func(r *http.Request) { r.AddCookie(&http.Cookie{Name: "token", Value: valid}) }, http.StatusOK},
		{"expired", func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+expired) }, http.StatusUnauthorized},
		{"signed with another key", func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+otherKey) }, http.StatusUnauthorized},
		{"garbage", func(r *http.Request) { r.Header.Set("Authorization", "Bearer not-a-jwt") }, http.StatusUnauthorized},
		{"Bearer undefined", func(r *http.Request) { r.Header.Set("Authorization", "Bearer undefined") }, http.StatusUnauthorized},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/engine-rest/process-definition/key/approval", nil)
			tc.setup(req)
			status, body := do(t, app, req)
			if status != tc.status {
				t.Fatalf("status = %d, want %d (body %q)", status, tc.status, body)
			}
			if status == http.StatusOK && body != userID {
				t.Fatalf("handler saw user %q, want %q", body, userID)
			}
		})
	}
}

// A browser WebSocket cannot send headers: the token rides in the query.
func TestCombinedAuth_TokenInQuery(t *testing.T) {
	app := newAuthApp(t, nil)
	userID := uuid.New()
	valid, err := authentication.NewJWTService(nil, testSecret).CreateAccessToken(models.User{ID: userID})
	if err != nil {
		t.Fatal(err)
	}
	status, body := do(t, app, httptest.NewRequest(http.MethodGet, "/ws/tasks?token="+valid, nil))
	if status != http.StatusOK || body != userID.String() {
		t.Fatalf("got %d %q", status, body)
	}
}

func TestCombinedAuth_PublicRouteNeedsNoCredentials(t *testing.T) {
	app := newAuthApp(t, nil)
	status, _ := do(t, app, httptest.NewRequest(http.MethodGet, "/health", nil))
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
}

func TestCombinedAuth_Basic(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	app := newAuthApp(t, sqlx.NewDb(sqlDB, "postgres"))

	basic := func(s string) string { return "Basic " + base64.StdEncoding.EncodeToString([]byte(s)) }

	// Malformed values are refused before any database lookup.
	for _, header := range []string{"Basic !!!not-base64", basic("no-colon")} {
		req := httptest.NewRequest(http.MethodGet, "/users/me", nil)
		req.Header.Set("Authorization", header)
		if status, _ := do(t, app, req); status != http.StatusUnauthorized {
			t.Fatalf("%q: status = %d, want 401", header, status)
		}
	}

	// Unknown user: one lookup, then 401 — never a fall-through to the handler.
	mock.ExpectQuery(regexp.QuoteMeta("users")).WillReturnError(sql.ErrNoRows)
	req := httptest.NewRequest(http.MethodGet, "/users/me", nil)
	req.Header.Set("Authorization", basic("nobody@example.com:secret"))
	if status, _ := do(t, app, req); status != http.StatusUnauthorized {
		t.Fatalf("unknown user: status = %d, want 401", status)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestBasicPasswordMatches_Cache(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("secret"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if !basicPasswordMatches(string(hash), "secret") {
		t.Fatal("right password refused")
	}
	// Second call is served by the cache.
	if !basicPasswordMatches(string(hash), "secret") {
		t.Fatal("cached password refused")
	}
	if basicPasswordMatches(string(hash), "wrong") {
		t.Fatal("wrong password accepted")
	}
	// A changed password (a new stored hash) never hits the old entry.
	newHash, err := bcrypt.GenerateFromPassword([]byte("other"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if basicPasswordMatches(string(newHash), "secret") {
		t.Fatal("old password accepted after a password change")
	}
}
