package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/ifty-r/upstream-ops/backend/storage"
)

type saveSearchConditionInput struct {
	Field storage.SavedSearchConditionField `json:"field" binding:"required"`
	Value string                            `json:"value" binding:"required"`
}

func registerPublicSearchConditions(g *gin.RouterGroup, d *Deps) {
	g.GET("/search-conditions", func(c *gin.Context) { listSearchConditions(c, d) })
}

func registerSearchConditions(g *gin.RouterGroup, d *Deps) {
	g.POST("/search-conditions", func(c *gin.Context) { saveSearchCondition(c, d) })
	g.DELETE("/search-conditions/:id", func(c *gin.Context) { deleteSearchCondition(c, d) })
}

func listSearchConditions(c *gin.Context, d *Deps) {
	repo, ok := searchConditionsRepo(c, d)
	if !ok {
		return
	}
	fields, ok := parseSearchConditionFields(c)
	if !ok {
		return
	}
	list, err := repo.List(fields)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": list})
}

func saveSearchCondition(c *gin.Context, d *Deps) {
	if !requireAuthenticatedSubject(c) {
		return
	}
	repo, ok := searchConditionsRepo(c, d)
	if !ok {
		return
	}
	var in saveSearchConditionInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	condition, err := repo.Save(in.Field, in.Value)
	if err != nil {
		fail(c, searchConditionErrorStatus(err), err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": condition})
}

func deleteSearchCondition(c *gin.Context, d *Deps) {
	if !requireAuthenticatedSubject(c) {
		return
	}
	repo, ok := searchConditionsRepo(c, d)
	if !ok {
		return
	}
	id, ok := parseUintParam(c, "id")
	if !ok {
		return
	}
	if err := repo.Delete(id); err != nil {
		fail(c, searchConditionErrorStatus(err), err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"deleted": true}})
}

func searchConditionsRepo(c *gin.Context, d *Deps) (*storage.SavedSearchConditions, bool) {
	if d == nil || d.SearchConditions == nil {
		fail(c, http.StatusServiceUnavailable, errors.New("saved search conditions repository is not configured"))
		return nil, false
	}
	return d.SearchConditions, true
}

func parseSearchConditionFields(c *gin.Context) ([]storage.SavedSearchConditionField, bool) {
	raw := strings.TrimSpace(c.Query("fields"))
	if raw == "" {
		return nil, true
	}
	seen := map[storage.SavedSearchConditionField]struct{}{}
	fields := make([]storage.SavedSearchConditionField, 0, 3)
	for _, part := range strings.Split(raw, ",") {
		field := storage.SavedSearchConditionField(strings.TrimSpace(part))
		if field == "" {
			continue
		}
		if !storage.IsSavedSearchConditionField(field) {
			fail(c, http.StatusBadRequest, fmt.Errorf("invalid saved search condition field: %s", field))
			return nil, false
		}
		if _, ok := seen[field]; ok {
			continue
		}
		seen[field] = struct{}{}
		fields = append(fields, field)
	}
	return fields, true
}

func requireAuthenticatedSubject(c *gin.Context) bool {
	if sub, ok := c.Get("authSubject"); ok {
		if text, ok := sub.(string); ok && strings.TrimSpace(text) != "" {
			return true
		}
	}
	c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing token"})
	return false
}

func searchConditionErrorStatus(err error) int {
	if err == nil {
		return http.StatusOK
	}
	text := err.Error()
	if strings.Contains(text, "invalid saved search condition field") ||
		strings.Contains(text, "value is required") ||
		strings.Contains(text, "id is required") {
		return http.StatusBadRequest
	}
	return http.StatusInternalServerError
}
