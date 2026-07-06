package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/ifty-r/upstream-ops/backend/config"
	"github.com/ifty-r/upstream-ops/backend/global"
)

const (
	githubRepoURL        = "https://github.com/ifty-r/upstream-ops"
	defaultGitHubTagsURL = "https://api.github.com/repos/ifty-r/upstream-ops/tags?per_page=100"
)

var (
	githubTagsURL       = defaultGitHubTagsURL
	githubVersionClient = &http.Client{Timeout: 2 * time.Second}
)

type versionResponse struct {
	Name            string `json:"name"`
	Title           string `json:"title"`
	Version         string `json:"version"`
	LatestVersion   string `json:"latest_version"`
	UpdateAvailable bool   `json:"update_available"`
	RepoURL         string `json:"repo_url"`
	ReleaseURL      string `json:"release_url"`
	UpdateError     string `json:"update_error"`
}

type githubTagResponse struct {
	Name string `json:"name"`
}

func registerVersion(api *gin.RouterGroup, d *Deps) {
	api.GET("/version", func(c *gin.Context) {
		force := c.Query("force") == "1" || strings.EqualFold(c.Query("force"), "true")
		c.JSON(http.StatusOK, buildVersionResponse(c.Request.Context(), d, force))
	})
}

func buildVersionResponse(ctx context.Context, d *Deps, force bool) versionResponse {
	app := config.AppConfig{Title: "UpstreamOps"}
	proxyCfg := config.ProxyConfig{}
	if d != nil && d.Runtime != nil {
		if cfg, err := config.LoadFile(d.Runtime.ConfigPath()); err == nil {
			app = cfg.App
		}
		proxyCfg = d.Runtime.CurrentProxy()
	}

	resp := versionResponse{
		Name:    "upstream-ops",
		Title:   app.Title,
		Version: global.VERSION,
		RepoURL: githubRepoURL,
	}

	latest, tagURL, err := fetchLatestGitHubTag(ctx, versionCheckClient(proxyCfg, force))
	if err != nil {
		resp.UpdateError = err.Error()
		return resp
	}
	resp.LatestVersion = latest
	resp.ReleaseURL = tagURL
	resp.UpdateAvailable = isVersionNewer(latest, global.VERSION)
	return resp
}

func versionCheckClient(proxyCfg config.ProxyConfig, force bool) *http.Client {
	if !proxyCfg.VersionCheckEnabled && !force {
		return githubVersionClient
	}
	proxyURL, err := proxyCfg.ActiveURL()
	if err != nil || proxyURL == "" {
		return githubVersionClient
	}
	u, err := url.Parse(proxyURL)
	if err != nil {
		return githubVersionClient
	}
	return &http.Client{
		Timeout: githubVersionClient.Timeout,
		Transport: &http.Transport{
			Proxy: http.ProxyURL(u),
		},
	}
}

func fetchLatestGitHubTag(ctx context.Context, client *http.Client) (string, string, error) {
	if client == nil {
		client = githubVersionClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, githubTagsURL, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "upstream-ops")

	resp, err := client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", "", fmt.Errorf("github tags status %d", resp.StatusCode)
	}

	var tags []githubTagResponse
	if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
		return "", "", err
	}
	latest := latestSemverTag(tags)
	if latest == "" {
		return "", "", errors.New("github tags missing semver tag")
	}
	return latest, githubRepoURL + "/tree/" + url.PathEscape(latest), nil
}

func latestSemverTag(tags []githubTagResponse) string {
	latest := ""
	var latestVersion [3]int
	for _, tag := range tags {
		name := strings.TrimSpace(tag.Name)
		parsed, ok := parseVersion(name)
		if !ok {
			continue
		}
		if latest == "" || compareParsedVersion(parsed, latestVersion) > 0 {
			latest = name
			latestVersion = parsed
		}
	}
	return latest
}

func isVersionNewer(latest, current string) bool {
	lv, ok := parseVersion(latest)
	if !ok {
		return false
	}
	cv, ok := parseVersion(current)
	if !ok {
		return false
	}
	return compareParsedVersion(lv, cv) > 0
}

func compareParsedVersion(left, right [3]int) int {
	for i := range left {
		if left[i] > right[i] {
			return 1
		}
		if left[i] < right[i] {
			return -1
		}
	}
	return 0
}

func parseVersion(v string) ([3]int, bool) {
	var out [3]int
	v = strings.TrimSpace(strings.TrimPrefix(v, "v"))
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}
