package observations

import "time"

// ClusterStatus represents the computed connectivity status of a cluster.
type ClusterStatus string

const (
	StatusUnknown     ClusterStatus = "Unknown"
	StatusConnected   ClusterStatus = "Connected"
	StatusVerified    ClusterStatus = "Verified"
	StatusOperational ClusterStatus = "Operational"
	StatusUnreachable ClusterStatus = "Unreachable"
)

// Observation records the last connectivity test result for a cluster.
type Observation struct {
	LastTestAt           time.Time `json:"last_test_at"`
	LastTestStage        string    `json:"last_test_stage"`
	LastTestOutcome      string    `json:"last_test_outcome"`       // "success" or "failure"
	LastTestErrorCode    string    `json:"last_test_error_code"`    // e.g. "ERR_NETWORK"
	LastTestErrorMessage string    `json:"last_test_error_message"` // human-readable error
	LastTestDurationMs   int64     `json:"last_test_duration_ms"`
	LastSeenAt           time.Time `json:"last_seen_at"`
}

// StatusResult holds the computed status and metadata for UI rendering.
type StatusResult struct {
	Status     ClusterStatus `json:"status"`
	TestFailing bool         `json:"test_failing,omitempty"`
	LastTestAt time.Time     `json:"last_test_at,omitempty"`
	ErrorCode  string        `json:"error_code,omitempty"`
}
