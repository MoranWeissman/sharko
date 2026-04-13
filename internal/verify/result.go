package verify

// Step represents a single verification step within a stage.
type Step struct {
	Name   string `json:"name"`
	Status string `json:"status"` // "pass", "fail", "skipped"
	Detail string `json:"detail,omitempty"`
}

// Result holds the outcome of a connectivity verification stage.
type Result struct {
	Success       bool                   `json:"success"`
	Stage         string                 `json:"stage"`
	ErrorCode     ErrorCode              `json:"error_code,omitempty"`
	ErrorMessage  string                 `json:"error_message,omitempty"`
	DurationMs    int64                  `json:"duration_ms"`
	ServerVersion string                 `json:"server_version,omitempty"`
	Details       map[string]interface{} `json:"details,omitempty"`
	Steps         []Step                 `json:"steps,omitempty"`
}
