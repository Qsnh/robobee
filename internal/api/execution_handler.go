package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

func (s *Server) listWorkerExecutions(c *gin.Context) {
	workerID := c.Param("id")
	execs, err := s.executionStore.ListByWorkerID(workerID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, execs)
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

func (s *Server) listSessionExecutions(c *gin.Context) {
	sessionID := c.Param("sessionId")
	execs, err := s.executionStore.ListBySessionID(sessionID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, execs)
}

func (s *Server) replyExecution(c *gin.Context) {
	var req struct {
		Message string `json:"message" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	newExec, err := s.manager.ReplyExecution(c.Request.Context(), c.Param("id"), req.Message)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusAccepted, newExec)
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
