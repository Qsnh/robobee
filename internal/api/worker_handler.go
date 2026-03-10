package api

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
)

type createWorkerRequest struct {
	Name                string `json:"name" binding:"required"`
	Description         string `json:"description"`
	Prompt              string `json:"prompt"`
	ScheduleDescription string `json:"schedule_description"`
	ScheduleEnabled     bool   `json:"schedule_enabled"`
	WorkDir             string `json:"work_dir"`
}

func (s *Server) createWorker(c *gin.Context) {
	var req createWorkerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.ScheduleEnabled {
		if req.ScheduleDescription == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "schedule_description is required when schedule is enabled"})
			return
		}
		if req.Prompt == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "prompt is required when schedule is enabled"})
			return
		}
	}

	w, err := s.manager.CreateWorker(
		req.Name, req.Description, req.Prompt,
		req.ScheduleDescription, req.ScheduleEnabled, req.WorkDir,
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
		Name                string `json:"name"`
		Description         string `json:"description"`
		Prompt              string `json:"prompt"`
		ScheduleDescription string `json:"schedule_description"`
		ScheduleEnabled     *bool  `json:"schedule_enabled"`
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
	if req.ScheduleDescription != "" {
		cronExpression, err := s.manager.ResolveCron(c.Request.Context(), req.ScheduleDescription)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate cron expression: " + err.Error()})
			return
		}
		w.ScheduleDescription = req.ScheduleDescription
		w.CronExpression = cronExpression
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
