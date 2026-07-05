package api

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/ifty-r/upstream-ops/backend/autogroup"
	"github.com/ifty-r/upstream-ops/backend/storage"
	"gorm.io/gorm"
)

func registerAutoGroups(g *gin.RouterGroup, d *Deps) {
	gp := g.Group("/auto-groups")
	gp.GET("", func(c *gin.Context) { listAutoGroupPolicies(c, d) })
	gp.POST("", func(c *gin.Context) { createAutoGroupPolicy(c, d) })
	gp.GET("/summary", func(c *gin.Context) { getAutoGroupSummary(c, d) })
	gp.GET("/capabilities/:channel_id", func(c *gin.Context) { getAutoGroupCapabilities(c, d) })
	gp.POST("/reorder", func(c *gin.Context) { reorderAutoGroupPolicies(c, d) })
	gp.GET("/:id", func(c *gin.Context) { getAutoGroupPolicy(c, d) })
	gp.PUT("/:id", func(c *gin.Context) { updateAutoGroupPolicy(c, d) })
	gp.DELETE("/:id", func(c *gin.Context) { deleteAutoGroupPolicy(c, d) })
	gp.POST("/:id/evaluate", func(c *gin.Context) { evaluateAutoGroupPolicy(c, d) })
	gp.POST("/:id/pause", func(c *gin.Context) { pauseAutoGroupPolicy(c, d) })
	gp.POST("/:id/resume", func(c *gin.Context) { resumeAutoGroupPolicy(c, d) })
	gp.GET("/:id/candidates", func(c *gin.Context) { listAutoGroupCandidates(c, d) })
	gp.POST("/:id/candidates/:candidate_id/disable", func(c *gin.Context) { setAutoGroupCandidateDisabled(c, d, true) })
	gp.POST("/:id/candidates/:candidate_id/enable", func(c *gin.Context) { setAutoGroupCandidateDisabled(c, d, false) })
	gp.POST("/:id/candidates/:candidate_id/probe", func(c *gin.Context) { probeAutoGroupCandidate(c, d) })
	gp.POST("/:id/candidates/:candidate_id/circuit", func(c *gin.Context) { circuitAutoGroupCandidate(c, d) })
	gp.POST("/:id/candidates/:candidate_id/force-switch", func(c *gin.Context) { forceSwitchAutoGroupCandidate(c, d) })
	gp.GET("/:id/evaluation-logs", func(c *gin.Context) { listAutoGroupEvaluationLogs(c, d) })
	gp.GET("/:id/switch-logs", func(c *gin.Context) { listAutoGroupSwitchLogs(c, d) })

	g.GET("/channels/:id/auto-group-policy", func(c *gin.Context) { getChannelAutoGroupPolicy(c, d) })
	g.PUT("/channels/:id/auto-group-policy", func(c *gin.Context) { upsertChannelAutoGroupPolicy(c, d) })
	g.POST("/channels/:id/auto-group-policy/evaluate", func(c *gin.Context) { evaluateChannelAutoGroupPolicy(c, d) })
	g.GET("/channels/:id/auto-group-policies", func(c *gin.Context) { listChannelAutoGroupPolicies(c, d) })
	g.GET("/channels/:id/auto-group-capabilities", func(c *gin.Context) { getChannelAutoGroupCapabilities(c, d) })
}

func listAutoGroupPolicies(c *gin.Context, d *Deps) {
	svc, ok := requireAutoGroupService(c, d)
	if !ok {
		return
	}
	list, err := svc.ListPolicies()
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": list})
}

func createAutoGroupPolicy(c *gin.Context, d *Deps) {
	svc, ok := requireAutoGroupService(c, d)
	if !ok {
		return
	}
	var in autogroup.PolicyInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	view, err := svc.CreatePolicy(in)
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": view})
}

func reorderAutoGroupPolicies(c *gin.Context, d *Deps) {
	svc, ok := requireAutoGroupService(c, d)
	if !ok {
		return
	}
	var in struct {
		IDs []uint `json:"ids"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	list, err := svc.ReorderPolicies(in.IDs)
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": list})
}

func getAutoGroupSummary(c *gin.Context, d *Deps) {
	svc, ok := requireAutoGroupService(c, d)
	if !ok {
		return
	}
	summary, err := svc.Summary()
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": summary})
}

func getAutoGroupCapabilities(c *gin.Context, d *Deps) {
	channelID, err := uintParam(c, "channel_id")
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	detectAutoGroupCapabilities(c, d, channelID)
}

func getAutoGroupPolicy(c *gin.Context, d *Deps) {
	svc, ok := requireAutoGroupService(c, d)
	if !ok {
		return
	}
	id, err := uintParam(c, "id")
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	view, err := svc.GetPolicy(id)
	if err != nil {
		fail(c, http.StatusNotFound, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": view})
}

func updateAutoGroupPolicy(c *gin.Context, d *Deps) {
	svc, ok := requireAutoGroupService(c, d)
	if !ok {
		return
	}
	id, err := uintParam(c, "id")
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	var in autogroup.PolicyInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	view, err := svc.UpdatePolicy(id, in)
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": view})
}

func deleteAutoGroupPolicy(c *gin.Context, d *Deps) {
	svc, ok := requireAutoGroupService(c, d)
	if !ok {
		return
	}
	id, err := uintParam(c, "id")
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if err := svc.DeletePolicy(id); err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func evaluateAutoGroupPolicy(c *gin.Context, d *Deps) {
	svc, ok := requireAutoGroupService(c, d)
	if !ok {
		return
	}
	id, err := uintParam(c, "id")
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	res, err := svc.EvaluatePolicy(c.Request.Context(), id)
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": res})
}

func pauseAutoGroupPolicy(c *gin.Context, d *Deps) {
	setAutoGroupPolicyEnabled(c, d, false)
}

func resumeAutoGroupPolicy(c *gin.Context, d *Deps) {
	setAutoGroupPolicyEnabled(c, d, true)
}

func setAutoGroupPolicyEnabled(c *gin.Context, d *Deps, enabled bool) {
	svc, ok := requireAutoGroupService(c, d)
	if !ok {
		return
	}
	id, err := uintParam(c, "id")
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	view, err := svc.SetPolicyEnabled(id, enabled)
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": view})
}

func listAutoGroupCandidates(c *gin.Context, d *Deps) {
	repo, ok := requireAutoGroupRepo(c, d)
	if !ok {
		return
	}
	id, err := uintParam(c, "id")
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	list, err := repo.ListCandidates(id)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": list})
}

func setAutoGroupCandidateDisabled(c *gin.Context, d *Deps, disabled bool) {
	svc, ok := requireAutoGroupService(c, d)
	if !ok {
		return
	}
	id, candidateID, ok := autoGroupCandidateParams(c)
	if !ok {
		return
	}
	res, err := svc.SetCandidateManualDisabled(id, candidateID, disabled)
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": res})
}

func probeAutoGroupCandidate(c *gin.Context, d *Deps) {
	svc, ok := requireAutoGroupService(c, d)
	if !ok {
		return
	}
	id, candidateID, ok := autoGroupCandidateParams(c)
	if !ok {
		return
	}
	res, err := svc.ProbeCandidate(c.Request.Context(), id, candidateID)
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": res})
}

func circuitAutoGroupCandidate(c *gin.Context, d *Deps) {
	svc, ok := requireAutoGroupService(c, d)
	if !ok {
		return
	}
	id, candidateID, ok := autoGroupCandidateParams(c)
	if !ok {
		return
	}
	res, err := svc.OpenCandidateCircuit(id, candidateID)
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": res})
}

func forceSwitchAutoGroupCandidate(c *gin.Context, d *Deps) {
	svc, ok := requireAutoGroupService(c, d)
	if !ok {
		return
	}
	id, candidateID, ok := autoGroupCandidateParams(c)
	if !ok {
		return
	}
	res, err := svc.ForceSwitchCandidate(c.Request.Context(), id, candidateID)
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": res})
}

func autoGroupCandidateParams(c *gin.Context) (uint, uint, bool) {
	id, err := uintParam(c, "id")
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return 0, 0, false
	}
	candidateID, err := uintParam(c, "candidate_id")
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return 0, 0, false
	}
	return id, candidateID, true
}

func listAutoGroupEvaluationLogs(c *gin.Context, d *Deps) {
	repo, ok := requireAutoGroupRepo(c, d)
	if !ok {
		return
	}
	id, err := uintParam(c, "id")
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	page, pageSize, err := parsePageQuery(c)
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	list, total, err := repo.ListEvaluationLogs(id, page, pageSize)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": pageResult(list, total, page, pageSize)})
}

func listAutoGroupSwitchLogs(c *gin.Context, d *Deps) {
	repo, ok := requireAutoGroupRepo(c, d)
	if !ok {
		return
	}
	id, err := uintParam(c, "id")
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	page, pageSize, err := parsePageQuery(c)
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	list, total, err := repo.ListSwitchLogs(id, page, pageSize)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": pageResult(list, total, page, pageSize)})
}

func getChannelAutoGroupPolicy(c *gin.Context, d *Deps) {
	svc, ok := requireAutoGroupService(c, d)
	if !ok {
		return
	}
	channelID, err := uintParam(c, "id")
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	view, err := svc.GetPolicyByChannel(channelID)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	if view == nil {
		c.JSON(http.StatusOK, gin.H{"data": nil})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": view})
}

func listChannelAutoGroupPolicies(c *gin.Context, d *Deps) {
	svc, ok := requireAutoGroupService(c, d)
	if !ok {
		return
	}
	channelID, err := uintParam(c, "id")
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	list, err := svc.ListPoliciesByChannel(channelID)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": list})
}

func getChannelAutoGroupCapabilities(c *gin.Context, d *Deps) {
	channelID, err := uintParam(c, "id")
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	detectAutoGroupCapabilities(c, d, channelID)
}

func detectAutoGroupCapabilities(c *gin.Context, d *Deps, channelID uint) {
	svc, ok := requireAutoGroupService(c, d)
	if !ok {
		return
	}
	matrix, err := svc.DetectCapabilities(c.Request.Context(), channelID)
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": matrix})
}

func upsertChannelAutoGroupPolicy(c *gin.Context, d *Deps) {
	svc, ok := requireAutoGroupService(c, d)
	if !ok {
		return
	}
	channelID, err := uintParam(c, "id")
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	var in autogroup.PolicyInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	in.ChannelID = channelID
	targetKeyName := in.TargetKeyName
	if targetKeyName == "" {
		targetKeyName = "auto"
	}
	existing, err := svc.GetPolicyByChannelTarget(channelID, targetKeyName)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	var view *autogroup.PolicyView
	if existing == nil {
		view, err = svc.CreatePolicy(in)
	} else {
		view, err = svc.UpdatePolicy(existing.ID, in)
	}
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": view})
}

func evaluateChannelAutoGroupPolicy(c *gin.Context, d *Deps) {
	svc, ok := requireAutoGroupService(c, d)
	if !ok {
		return
	}
	repo, ok := requireAutoGroupRepo(c, d)
	if !ok {
		return
	}
	channelID, err := uintParam(c, "id")
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	p, err := repo.FindPolicyByChannel(channelID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		fail(c, http.StatusNotFound, fmt.Errorf("该渠道还没有智能分组策略"))
		return
	}
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	res, err := svc.EvaluatePolicy(c.Request.Context(), p.ID)
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": res})
}

func requireAutoGroupService(c *gin.Context, d *Deps) (*autogroup.Service, bool) {
	if d == nil || d.AutoGroup == nil {
		fail(c, http.StatusServiceUnavailable, fmt.Errorf("智能分组服务未初始化"))
		return nil, false
	}
	return d.AutoGroup, true
}

func requireAutoGroupRepo(c *gin.Context, d *Deps) (*storage.AutoGroups, bool) {
	if d == nil || d.AutoGroups == nil {
		fail(c, http.StatusServiceUnavailable, fmt.Errorf("智能分组存储未初始化"))
		return nil, false
	}
	return d.AutoGroups, true
}

func pageResult[T any](items []T, total int64, page, pageSize int) gin.H {
	pages := 1
	if total > 0 {
		pages = int((total + int64(pageSize) - 1) / int64(pageSize))
	}
	return gin.H{
		"items":     items,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
		"pages":     pages,
	}
}
