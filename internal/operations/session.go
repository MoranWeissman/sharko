package operations

import "time"

// Status represents the lifecycle state of an operation session.
type Status string

const (
	StatusPending   Status = "pending"
	StatusRunning   Status = "running"
	StatusWaiting   Status = "waiting"   // waiting for external event (PR merge)
	StatusCompleted Status = "completed"
	StatusFailed    Status = "failed"
	StatusCancelled Status = "cancelled"
)

// Step is a named stage within an operation.
type Step struct {
	Name    string `json:"name"`
	Status  Status `json:"status"`
	Message string `json:"message,omitempty"`
}

// Session tracks a long-running operation.
type Session struct {
	ID            string    `json:"id"`
	Type          string    `json:"type"` // "init", "add-cluster", etc.
	Status        Status    `json:"status"`
	Steps         []Step    `json:"steps"`
	CurrentStep   int       `json:"current_step"`
	PRUrl         string    `json:"pr_url,omitempty"`
	PRID          int       `json:"pr_id,omitempty"`
	Result        any       `json:"result,omitempty"`
	Error         string    `json:"error,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	LastHeartbeat time.Time `json:"last_heartbeat"`
}

// IsAlive returns true if a heartbeat was received within the threshold.
func (s *Session) IsAlive(threshold time.Duration) bool {
	return time.Since(s.LastHeartbeat) < threshold
}
