package api

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/robobee/core/internal/model"
)

type createWorkerRequest struct {
	Name             string            `json:"name" binding:"required"`
	Description      string            `json:"description"`
	Prompt           string            `json:"prompt" binding:"required"`
	RuntimeType      model.RuntimeType `json:"runtime_type"`
	TriggerType      model.TriggerType `json:"trigger_type" binding:"required"`
	CronExpression   string            `json:"cron_expression"`
	Recipients       []string          `json:"recipients"`
	RequiresApproval bool              `json:"requires_approval"`
}

func (s *Server) createWorker(c *gin.Context) {
	var req createWorkerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.RuntimeType == "" {
		req.RuntimeType = model.RuntimeClaudeCode
	}

	if req.TriggerType == model.TriggerCron && req.CronExpression == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cron_expression is required for cron trigger type"})
		return
	}

	if req.Recipients == nil {
		req.Recipients = []string{}
	}

	w, err := s.manager.CreateWorker(
		req.Name, req.Description, req.Prompt,
		req.RuntimeType, req.TriggerType,
		req.CronExpression, req.Recipients, req.RequiresApproval,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, w)
}

func (s *Server) listWorkers(c *gin.Context) {
	workers, err := s.workerStore.List()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, workers)
}

func (s *Server) getWorker(c *gin.Context) {
	w, err := s.workerStore.GetByID(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "worker not found"})
		return
	}
	c.JSON(http.StatusOK, w)
}

func (s *Server) updateWorker(c *gin.Context) {
	w, err := s.workerStore.GetByID(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "worker not found"})
		return
	}

	var req struct {
		Name             string            `json:"name"`
		Description      string            `json:"description"`
		Prompt           string            `json:"prompt"`
		RuntimeType      model.RuntimeType `json:"runtime_type"`
		TriggerType      model.TriggerType `json:"trigger_type"`
		CronExpression   string            `json:"cron_expression"`
		Recipients       []string          `json:"recipients"`
		RequiresApproval *bool             `json:"requires_approval"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.Name != "" {
		w.Name = req.Name
	}
	if req.Description != "" {
		w.Description = req.Description
	}
	if req.Prompt != "" {
		w.Prompt = req.Prompt
	}
	if req.RuntimeType != "" {
		w.RuntimeType = req.RuntimeType
	}
	if req.TriggerType != "" {
		w.TriggerType = req.TriggerType
	}
	if req.CronExpression != "" {
		w.CronExpression = req.CronExpression
	}
	if req.Recipients != nil {
		recipients, _ := json.Marshal(req.Recipients)
		w.Recipients = recipients
	}
	if req.RequiresApproval != nil {
		w.RequiresApproval = *req.RequiresApproval
	}

	updated, err := s.workerStore.Update(w)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, updated)
}

func (s *Server) deleteWorker(c *gin.Context) {
	if err := s.workerStore.Delete(c.Param("id")); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}

func (s *Server) sendMessage(c *gin.Context) {
	workerID := c.Param("id")

	worker, err := s.workerStore.GetByID(workerID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "worker not found"})
		return
	}

	if worker.TriggerType != model.TriggerMessage {
		c.JSON(http.StatusBadRequest, gin.H{"error": "this worker is not message-triggered"})
		return
	}

	var req struct {
		Message string `json:"message" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	exec, err := s.manager.ExecuteWorker(context.Background(), workerID, req.Message)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusAccepted, exec)
}
