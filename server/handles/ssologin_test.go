package handles

import (
	"net/http/httptest"
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
