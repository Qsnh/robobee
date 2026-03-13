package dispatcher

import "github.com/robobee/core/internal/platform"

// DispatchTask is the unit of work sent to the Dispatcher by the TaskScheduler
// (for task-based dispatch) or directly by the Feeder (for clear commands).
type DispatchTask struct {
	TaskID          string                 // empty for clear commands
	WorkerID        string
	SessionKey      string                 // original message session_key
	Instruction     string
	ReplyTo         platform.InboundMessage // platform info for result delivery
	TaskType        string                 // "immediate"|"countdown"|"scheduled"|"clear"
	MessageID       string                 // originating platform_messages.id (for session lookup)
	ReplySessionKey string                 // overrides ReplyTo session key if non-empty
}
