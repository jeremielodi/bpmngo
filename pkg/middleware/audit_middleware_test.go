package middleware

import (
	"strings"
	"testing"
)

func TestRedactBody(t *testing.T) {
	cases := map[string]string{
		`{"email":"a@b.c","password":"hunter2"}`:                         "hunter2",
		`{"current_password":"old-secret","new_password":"new-secret"}`:  "secret",
		`{"Password":"MiXeD"}`:                                           "MiXeD",
		`{"user":{"credentials":{"password":"deep"}}}`:                   "deep",
		`{"items":[{"refresh_token":"tok-1"},{"client_secret":"cs-2"}]}`: "tok-",
		`email=a@b.c&password=formpass`:                                  "formpass",
	}
	for body, leaked := range cases {
		got := redactBody([]byte(body))
		if strings.Contains(got, leaked) {
			t.Errorf("redactBody(%s) = %s: still contains %q", body, got, leaked)
		}
	}
	// What is not sensitive is kept as it was.
	if got := redactBody([]byte(`{"email":"a@b.c","variables":{"amount":5}}`)); !strings.Contains(got, `"email":"a@b.c"`) || !strings.Contains(got, `"amount":5`) {
		t.Errorf("non-sensitive fields must be kept, got %s", got)
	}
}
