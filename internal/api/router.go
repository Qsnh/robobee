package api

import (
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/robobee/core/internal/store"
	"github.com/robobee/core/internal/worker"
)

type Server struct {
	router         *gin.Engine
	workerStore    *store.WorkerStore
	taskStore      *store.TaskStore
	executionStore *store.ExecutionStore
	emailStore     *store.EmailStore
	memoryStore    *store.MemoryStore
	manager        *worker.Manager
}

func NewServer(
	ws *store.WorkerStore,
	ts *store.TaskStore,
	es *store.ExecutionStore,
	emailS *store.EmailStore,
	ms *store.MemoryStore,
	mgr *worker.Manager,
) *Server {
	router := gin.Default()
	router.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"*"},
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: false,
	}))

	s := &Server{
		router:         router,
		workerStore:    ws,
		taskStore:      ts,
		executionStore: es,
		emailStore:     emailS,
		memoryStore:    ms,
		manager:        mgr,
	}
	s.setupRoutes()
	return s
}

func (s *Server) setupRoutes() {
	api := s.router.Group("/api")
	{
		// Workers
		api.POST("/workers", s.createWorker)
		api.GET("/workers", s.listWorkers)
		api.GET("/workers/:id", s.getWorker)
		api.PUT("/workers/:id", s.updateWorker)
		api.DELETE("/workers/:id", s.deleteWorker)

		// Tasks
		api.POST("/workers/:id/tasks", s.createTask)
		api.GET("/workers/:id/tasks", s.listTasks)
		api.PUT("/tasks/:id", s.updateTask)
		api.DELETE("/tasks/:id", s.deleteTask)

		// Executions
		api.POST("/tasks/:id/execute", s.executeTask)
		api.GET("/executions", s.listExecutions)
		api.GET("/executions/:id", s.getExecution)
		api.POST("/executions/:id/approve", s.approveExecution)
		api.POST("/executions/:id/reject", s.rejectExecution)

		// Message trigger
		api.POST("/workers/:id/message", s.sendMessage)

		// Emails
		api.GET("/executions/:id/emails", s.listEmails)

		// WebSocket logs
		api.GET("/executions/:id/logs", s.streamLogs)
	}
}

func (s *Server) Run(addr string) error {
	return s.router.Run(addr)
}
