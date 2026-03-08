package api

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/robobee/core/internal/model"
)

type createWorkerRequest struct {
	Name            string            `json:"name" binding:"required"`
	Description     string            `json:"description"`
	Prompt          string            `json:"prompt"`
	RuntimeType     model.RuntimeType `json:"runtime_type"`
	CronExpression  string            `json:"cron_expression"`
	ScheduleEnabled bool              `json:"schedule_enabled"`
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

	if req.ScheduleEnabled {
		if req.CronExpression == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "cron_expression is required when schedule is enabled"})
			return
		}
		if req.Prompt == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "prompt is required when schedule is enabled"})
			return
		}
	}

	w, err := s.manager.CreateWorker(
		req.Name, req.Description, req.Prompt,
		req.RuntimeType,
		req.CronExpression, req.ScheduleEnabled,
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
		Name            string            `json:"name"`
		Description     string            `json:"description"`
		Prompt          string            `json:"prompt"`
		RuntimeType     model.RuntimeType `json:"runtime_type"`
		CronExpression  string            `json:"cron_expression"`
		ScheduleEnabled *bool             `json:"schedule_enabled"`
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
	if req.CronExpression != "" {
		w.CronExpression = req.CronExpression
	}
	if req.ScheduleEnabled != nil {
		w.ScheduleEnabled = *req.ScheduleEnabled
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

	_, err := s.workerStore.GetByID(workerID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "worker not found"})
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
