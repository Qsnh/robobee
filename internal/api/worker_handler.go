package api

import (
	"context"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/robobee/core/internal/model"
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
			c.JSON(http.StatusBadRequest, gin.H{"error": localize(c, "ScheduleDescriptionRequired")})
			return
		}
		if req.Prompt == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": localize(c, "PromptRequired")})
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

	s.applySchedule(w)

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
		c.JSON(http.StatusNotFound, gin.H{"error": localize(c, "WorkerNotFound")})
		return
	}
	c.JSON(http.StatusOK, w)
}

func (s *Server) updateWorker(c *gin.Context) {
	w, err := s.workerStore.GetByID(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": localize(c, "WorkerNotFound")})
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
			c.JSON(http.StatusInternalServerError, gin.H{"error": localizeWithData(c, "FailedToGenerateCronExpression", map[string]string{"Error": err.Error()})})
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

	s.applySchedule(updated)

	c.JSON(http.StatusOK, updated)
}

func (s *Server) deleteWorker(c *gin.Context) {
	id := c.Param("id")
	deleteWorkDir := c.Query("delete_work_dir") == "true"
	if err := s.manager.DeleteWorker(id, deleteWorkDir); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	s.scheduler.RemoveWorker(id)
	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}

func (s *Server) applySchedule(w model.Worker) {
	if w.ScheduleEnabled && w.CronExpression != "" {
		if err := s.scheduler.AddWorker(w.ID, w.CronExpression); err != nil {
			log.Printf("failed to schedule worker %s: %v", w.ID, err)
		}
	} else {
		s.scheduler.RemoveWorker(w.ID)
	}
}

func (s *Server) sendMessage(c *gin.Context) {
	workerID := c.Param("id")

	_, err := s.workerStore.GetByID(workerID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": localize(c, "WorkerNotFound")})
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
