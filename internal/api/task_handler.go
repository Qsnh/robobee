package api

import (
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/robobee/core/internal/model"
)

type createTaskRequest struct {
	Name             string            `json:"name" binding:"required"`
	Plan             string            `json:"plan" binding:"required"`
	TriggerType      model.TriggerType `json:"trigger_type"`
	CronExpression   string            `json:"cron_expression"`
	Recipients       []string          `json:"recipients" binding:"required,min=1"`
	RequiresApproval bool              `json:"requires_approval"`
}

func (s *Server) createTask(c *gin.Context) {
	workerID := c.Param("id")

	// Verify worker exists
	if _, err := s.workerStore.GetByID(workerID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "worker not found"})
		return
	}

	var req createTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.TriggerType == "" {
		req.TriggerType = model.TriggerManual
	}

	recipients, _ := json.Marshal(req.Recipients)
	task, err := s.taskStore.Create(model.Task{
		WorkerID:         workerID,
		Name:             req.Name,
		Plan:             req.Plan,
		TriggerType:      req.TriggerType,
		CronExpression:   req.CronExpression,
		Recipients:       recipients,
		RequiresApproval: req.RequiresApproval,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, task)
}

func (s *Server) listTasks(c *gin.Context) {
	tasks, err := s.taskStore.ListByWorkerID(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, tasks)
}

func (s *Server) updateTask(c *gin.Context) {
	task, err := s.taskStore.GetByID(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "task not found"})
		return
	}

	var req struct {
		Name             string   `json:"name"`
		Plan             string   `json:"plan"`
		TriggerType      string   `json:"trigger_type"`
		CronExpression   string   `json:"cron_expression"`
		Recipients       []string `json:"recipients"`
		RequiresApproval *bool    `json:"requires_approval"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.Name != "" {
		task.Name = req.Name
	}
	if req.Plan != "" {
		task.Plan = req.Plan
	}
	if req.TriggerType != "" {
		task.TriggerType = model.TriggerType(req.TriggerType)
	}
	if req.CronExpression != "" {
		task.CronExpression = req.CronExpression
	}
	if req.Recipients != nil {
		recipients, _ := json.Marshal(req.Recipients)
		task.Recipients = recipients
	}
	if req.RequiresApproval != nil {
		task.RequiresApproval = *req.RequiresApproval
	}

	updated, err := s.taskStore.Update(task)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, updated)
}

func (s *Server) deleteTask(c *gin.Context) {
	if err := s.taskStore.Delete(c.Param("id")); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}
