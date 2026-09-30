package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSessionRequiresCSRFAndSameOriginAndLogoutRevokesIt(t *testing.T) {
	fixture := newSecurityFixture(t)
	auth := fixture.auth
	login := httptest.NewRequest(http.MethodPost, "https://admin.example.test/api/login", strings.NewReader(`{"password":"synthetic-test-password"}`))
	login.Header.Set("Content-Type", "application/json")
	login.Header.Set("Origin", "https://admin.example.test")
	response := httptest.NewRecorder()
	auth.loginHandler(response, login)
	if response.Code != http.StatusOK {
		t.Fatalf("login: %d %s", response.Code, response.Body.String())
	}
	var sessionData struct {
		Authenticated bool   `json:"authenticated"`
		CSRF          string `json:"csrf_token"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &sessionData); err != nil {
		t.Fatal(err)
	}
	cookies := response.Result().Cookies()
	if !sessionData.Authenticated || sessionData.CSRF == "" || len(cookies) != 1 {
		t.Fatal("login did not establish a usable session")
	}
	cookie := cookies[0]
	if !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatal("session cookie lacks transport/script/cross-site isolation")
	}
	// Restarting authentication keeps the durable session usable without
	// keeping a plaintext token or an in-memory session map on the server.
	restarted, err := newAuthentication("synthetic-test-password", "https://admin.example.test", fixture.db, fixture.vault)
	if err != nil {
		t.Fatal(err)
	}
	auth = restarted
	mutations := 0
	mutate := auth.protect(func(w http.ResponseWriter, r *http.Request) { mutations++; w.WriteHeader(http.StatusNoContent) })
	request := func(token, origin string) *http.Request {
		r := httptest.NewRequest(http.MethodPut, "https://admin.example.test/api/secrets/example", nil)
		r.AddCookie(cookie)
		r.Header.Set("X-CSRF-Token", token)
		r.Header.Set("Origin", origin)
		return r
	}
	for _, r := range []*http.Request{request("", "https://admin.example.test"), request(sessionData.CSRF, "https://other.example.test")} {
		w := httptest.NewRecorder()
		mutate(w, r)
		if w.Code != http.StatusForbidden || mutations != 0 {
			t.Fatal("untrusted request changed protected state")
		}
	}
	accepted := httptest.NewRecorder()
	mutate(accepted, request(sessionData.CSRF, "https://admin.example.test"))
	if accepted.Code != http.StatusNoContent || mutations != 1 {
		t.Fatal("authenticated same-origin request did not succeed")
	}
	logout := httptest.NewRecorder()
	auth.protect(auth.logoutHandler)(logout, request(sessionData.CSRF, "https://admin.example.test"))
	if logout.Code != http.StatusNoContent {
		t.Fatal("logout failed")
	}
	replay := httptest.NewRecorder()
	mutate(replay, request(sessionData.CSRF, "https://admin.example.test"))
	if replay.Code != http.StatusUnauthorized || mutations != 1 {
		t.Fatal("revoked session could mutate state")
	}
}
