package handles

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestDisableSSOCache(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)

	disableSSOCache(context)

	want := map[string]string{
		"Cache-Control": "no-store, no-cache, must-revalidate, max-age=0",
		"Pragma":        "no-cache",
		"Expires":       "0",
	}
	for name, value := range want {
		if got := recorder.Header().Get(name); got != value {
			t.Fatalf("%s = %q, want %q", name, got, value)
		}
	}
}

func TestStateSessionCookieIsStable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	firstRecorder := httptest.NewRecorder()
	firstContext, _ := gin.CreateTestContext(firstRecorder)
	firstContext.Request = httptest.NewRequest(http.MethodGet, "/api/auth/sso", nil)
	firstContext.Request.Header.Set("X-Forwarded-Proto", "https")
	firstSession := ensureStateSession(firstContext)
	cookies := firstRecorder.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("state session cookies = %d, want 1", len(cookies))
	}
	cookie := cookies[0]
	if cookie.Value != firstSession || !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("unexpected state session cookie: %#v", cookie)
	}

	secondRecorder := httptest.NewRecorder()
	secondContext, _ := gin.CreateTestContext(secondRecorder)
	secondContext.Request = httptest.NewRequest(http.MethodGet, "/api/auth/sso", nil)
	secondContext.Request.AddCookie(cookie)
	if secondSession := ensureStateSession(secondContext); secondSession != firstSession {
		t.Fatal("state session changed after returning the cookie")
	}
	if len(secondRecorder.Result().Cookies()) != 0 {
		t.Fatal("existing valid state session cookie was replaced")
	}
}

func TestVerifyStateUsesSessionAndConsumesState(t *testing.T) {
	clientID := "oidc-client"
	session := strings.Repeat("s", stateSessionLength)
	state := generateState(clientID, session)
	if verifyState(clientID, "another-session", state) {
		t.Fatal("state accepted for another browser session")
	}
	if !verifyState(clientID, session, state) {
		t.Fatal("state rejected for its browser session")
	}
	if verifyState(clientID, session, state) {
		t.Fatal("state was accepted more than once")
	}
}
