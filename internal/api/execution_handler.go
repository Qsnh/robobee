package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/robobee/core/internal/model"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

func (s *Server) executeTask(c *gin.Context) {
	taskID := c.Param("id")

	exec, err := s.manager.ExecuteTask(c.Request.Context(), taskID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusAccepted, exec)
}

func (s *Server) listExecutions(c *gin.Context) {
	execs, err := s.executionStore.List()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, execs)
}

func (s *Server) getExecution(c *gin.Context) {
	exec, err := s.executionStore.GetByID(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "execution not found"})
		return
	}
	c.JSON(http.StatusOK, exec)
}

func (s *Server) approveExecution(c *gin.Context) {
	execID := c.Param("id")
	if err := s.executionStore.UpdateStatus(execID, model.ExecStatusApproved); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "approved"})
}

func (s *Server) rejectExecution(c *gin.Context) {
	execID := c.Param("id")

	var req struct {
		Feedback string `json:"feedback"`
	}
	c.ShouldBindJSON(&req)

	if err := s.executionStore.UpdateStatus(execID, model.ExecStatusRejected); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "rejected", "feedback": req.Feedback})
}

func (s *Server) sendMessage(c *gin.Context) {
	workerID := c.Param("id")

	var req struct {
		Message string `json:"message" binding:"required"`
		TaskID  string `json:"task_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// If task_id provided, execute that task; otherwise find first manual task
	taskID := req.TaskID
	if taskID == "" {
		tasks, err := s.taskStore.ListByWorkerID(workerID)
		if err != nil || len(tasks) == 0 {
			c.JSON(http.StatusNotFound, gin.H{"error": "no tasks found for worker"})
			return
		}
		taskID = tasks[0].ID
	}

	exec, err := s.manager.ExecuteTask(c.Request.Context(), taskID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusAccepted, exec)
}

func (s *Server) listEmails(c *gin.Context) {
	emails, err := s.emailStore.ListByExecutionID(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, emails)
}

func (s *Server) streamLogs(c *gin.Context) {
	execID := c.Param("id")

	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	ch := s.manager.SubscribeLogs(execID)
	for out := range ch {
		if err := conn.WriteJSON(out); err != nil {
			break
		}
	}
}
