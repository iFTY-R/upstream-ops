package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/ifty-r/upstream-ops/backend/global"
)

func TestIsVersionNewer(t *testing.T) {
	tests := []struct {
		latest  string
		current string
		want    bool
	}{
		{latest: "0.2.1", current: "0.2.0", want: true},
		{latest: "v0.2.1", current: "0.2.0", want: true},
		{latest: "0.2.0", current: "v0.2.0", want: false},
		{latest: "0.1.9", current: "0.2.0", want: false},
	}
	for _, tt := range tests {
		if got := isVersionNewer(tt.latest, tt.current); got != tt.want {
			t.Fatalf("isVersionNewer(%q, %q) = %v, want %v", tt.latest, tt.current, got, tt.want)
		}
	}
}

func TestVersionEndpointReportsUpdate(t *testing.T) {
	gin.SetMode(gin.TestMode)

	withGitHubTagsServer(t, http.StatusOK, `[{"name":"v0.0.1"},{"name":"not-a-version"},{"name":"v999.0.0"},{"name":"v1.0.0"}]`)
	resp := requestVersion(t)

	if !resp.UpdateAvailable {
		t.Fatalf("update_available = false, want true")
	}
	if resp.LatestVersion != "v999.0.0" {
		t.Fatalf("latest_version = %q, want v999.0.0", resp.LatestVersion)
	}
	if resp.ReleaseURL == "" {
		t.Fatalf("release_url is empty")
	}
	if !strings.Contains(resp.ReleaseURL, "/tree/v999.0.0") {
		t.Fatalf("release_url = %q, want tag tree url", resp.ReleaseURL)
	}
}

func TestVersionEndpointReportsNoUpdate(t *testing.T) {
	gin.SetMode(gin.TestMode)

	withGitHubTagsServer(t, http.StatusOK, `[{"name":"`+global.VERSION+`"}]`)
	resp := requestVersion(t)

	if resp.UpdateAvailable {
		t.Fatalf("update_available = true, want false")
	}
	if resp.LatestVersion != global.VERSION {
		t.Fatalf("latest_version = %q, want %s", resp.LatestVersion, global.VERSION)
	}
}

func TestVersionEndpointKeepsResponseOnGitHubError(t *testing.T) {
	gin.SetMode(gin.TestMode)

	withGitHubTagsServer(t, http.StatusInternalServerError, `{"message":"error"}`)
	resp := requestVersion(t)

	if resp.UpdateAvailable {
		t.Fatalf("update_available = true, want false")
	}
	if strings.TrimSpace(resp.UpdateError) == "" {
		t.Fatalf("update_error is empty")
	}
	if resp.Version != global.VERSION {
		t.Fatalf("version = %q, want %s", resp.Version, global.VERSION)
	}
}

func TestVersionEndpointReportsTagErrorWhenNoSemverTag(t *testing.T) {
	gin.SetMode(gin.TestMode)

	withGitHubTagsServer(t, http.StatusOK, `[{"name":"latest"},{"name":"dev"}]`)
	resp := requestVersion(t)

	if resp.UpdateAvailable {
		t.Fatalf("update_available = true, want false")
	}
	if !strings.Contains(resp.UpdateError, "missing semver tag") {
		t.Fatalf("update_error = %q, want missing semver tag", resp.UpdateError)
	}
}

func withGitHubTagsServer(t *testing.T, status int, body string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	oldURL := githubTagsURL
	oldClient := githubVersionClient
	githubTagsURL = srv.URL
	githubVersionClient = srv.Client()
	t.Cleanup(func() {
		githubTagsURL = oldURL
		githubVersionClient = oldClient
	})
}

func requestVersion(t *testing.T) versionResponse {
	t.Helper()
	r := gin.New()
	registerVersion(r.Group("/api"), &Deps{})

	req := httptest.NewRequest(http.MethodGet, "/api/version", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp versionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return resp
}
