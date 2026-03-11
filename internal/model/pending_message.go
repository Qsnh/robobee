package model

// PendingMessage is a platform message recovered from the DB during startup.
// It represents an unfinished message that needs to be re-queued.
type PendingMessage struct {
	ID         string
	SessionKey string
	WorkerID   string
	Platform   string
	Content    string
}
