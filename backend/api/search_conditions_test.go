package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/ifty-r/upstream-ops/backend/auth"
	"github.com/ifty-r/upstream-ops/backend/storage"
)

func TestSearchConditionsPublicListAndValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openTestDB(t)
	repo := storage.NewSavedSearchConditions(db)
	if _, err := repo.Save(storage.SavedSearchConditionKeyword, "Alpha"); err != nil {
		t.Fatalf("save keyword: %v", err)
	}
	if _, err := repo.Save(storage.SavedSearchConditionCategoryName, "Cards"); err != nil {
		t.Fatalf("save category: %v", err)
	}
	router, _ := newSearchConditionsTestRouter(t, repo)

	resp := performRequest(router, http.MethodGet, "/api/public/search-conditions?fields=keyword")
	if resp.Code != http.StatusOK {
		t.Fatalf("public list status = %d, body=%s", resp.Code, resp.Body.String())
	}
	var body struct {
		Data []storage.SavedSearchCondition `json:"data"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(body.Data) != 1 || body.Data[0].Field != storage.SavedSearchConditionKeyword || body.Data[0].Value != "Alpha" {
		t.Fatalf("public list data = %#v", body.Data)
	}

	invalid := performRequest(router, http.MethodGet, "/api/public/search-conditions?fields=keyword,nope")
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid fields status = %d, want %d; body=%s", invalid.Code, http.StatusBadRequest, invalid.Body.String())
	}
}

func TestSearchConditionsMutationsRequireAuthAndDelete(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openTestDB(t)
	repo := storage.NewSavedSearchConditions(db)
	router, authService := newSearchConditionsTestRouter(t, repo)
	token, _, err := authService.Issue()
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}

	unauth := performJSONRequest(router, http.MethodPost, "/api/search-conditions", `{"field":"keyword","value":"Alpha"}`, "")
	if unauth.Code != http.StatusUnauthorized {
		t.Fatalf("unauth save status = %d, want %d", unauth.Code, http.StatusUnauthorized)
	}

	resp := performJSONRequest(router, http.MethodPost, "/api/search-conditions", `{"field":"keyword","value":"Alpha"}`, token)
	if resp.Code != http.StatusOK {
		t.Fatalf("save status = %d, body=%s", resp.Code, resp.Body.String())
	}
	var body struct {
		Data storage.SavedSearchCondition `json:"data"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode save response: %v", err)
	}
	if body.Data.ID == 0 || body.Data.Value != "Alpha" {
		t.Fatalf("saved data = %#v", body.Data)
	}

	empty := performJSONRequest(router, http.MethodPost, "/api/search-conditions", `{"field":"keyword","value":"   "}`, token)
	if empty.Code != http.StatusBadRequest {
		t.Fatalf("empty save status = %d, want %d; body=%s", empty.Code, http.StatusBadRequest, empty.Body.String())
	}

	del := performJSONRequest(router, http.MethodDelete, "/api/search-conditions/"+jsonID(body.Data.ID), "", token)
	if del.Code != http.StatusOK {
		t.Fatalf("delete status = %d, body=%s", del.Code, del.Body.String())
	}
	list, err := repo.List(nil)
	if err != nil {
		t.Fatalf("list after delete: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("list after delete = %#v", list)
	}
}

func TestSearchConditionMutationDoesNotBecomePublicWithoutAuthMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openTestDB(t)
	repo := storage.NewSavedSearchConditions(db)
	router := gin.New()
	registerSearchConditions(router.Group("/api"), &Deps{SearchConditions: repo})

	resp := performJSONRequest(router, http.MethodPost, "/api/search-conditions", `{"field":"keyword","value":"Alpha"}`, "")
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("save without auth middleware status = %d, want %d", resp.Code, http.StatusUnauthorized)
	}
}

func newSearchConditionsTestRouter(t *testing.T, repo *storage.SavedSearchConditions) (*gin.Engine, *auth.Service) {
	t.Helper()
	authService, err := auth.New("admin", "password", "test-secret", time.Hour)
	if err != nil {
		t.Fatalf("create auth service: %v", err)
	}
	deps := &Deps{SearchConditions: repo}
	router := gin.New()
	registerPublicSearchConditions(router.Group("/api/public"), deps)
	protected := router.Group("/api")
	protected.Use(authService.Middleware())
	registerSearchConditions(protected, deps)
	return router, authService
}

func performJSONRequest(router http.Handler, method, path, rawBody, token string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	var body *bytes.Buffer
	if rawBody == "" {
		body = bytes.NewBuffer(nil)
	} else {
		body = bytes.NewBufferString(rawBody)
	}
	req := httptest.NewRequest(method, path, body)
	if rawBody != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	router.ServeHTTP(recorder, req)
	return recorder
}

func jsonID(id uint) string {
	body, _ := json.Marshal(id)
	return string(body)
}
