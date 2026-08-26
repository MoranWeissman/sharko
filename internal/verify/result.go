package verify

// Step represents a single verification step within a stage.
type Step struct {
	Name   string `json:"name"`
	Status string `json:"status"` // "pass", "fail", "skipped"
	Detail string `json:"detail,omitempty"`
}

// Result holds the outcome of a connectivity verification stage.
//
// ErrorMessage is a catalog sentence from FriendlyMessage — never the
// cluster's own words. The cluster's own words live on diagnostic below.
type Result struct {
	Success       bool                   `json:"success"`
	Stage         string                 `json:"stage"`
	ErrorCode     ErrorCode              `json:"error_code,omitempty"`
	ErrorMessage  string                 `json:"error_message,omitempty"`
	DurationMs    int64                  `json:"duration_ms"`
	ServerVersion string                 `json:"server_version,omitempty"`
	Details       map[string]interface{} `json:"details,omitempty"`
	Steps         []Step                 `json:"steps,omitempty"`

	// diagnostic is the real cause, kept for the server-side log and for
	// nothing else.
	//
	// It is UNEXPORTED, and that is the safety property rather than a style
	// choice: encoding/json cannot see an unexported field, so no handler can
	// leak this into a response by adding it to a struct, embedding a Result,
	// or marshalling one directly. There is no json tag to get wrong and no
	// reviewer who has to notice. Reading it takes the explicit Diagnostic()
	// call below, which is easy to grep for and rare on purpose.
	//
	// It already went through credsafe.Sentence before it got here, so a
	// credentials-backend error is the fixed safe sentence even in the log.
	diagnostic string
}

// Diagnostic returns the real cause, for a server-side log line only.
//
// Never put this in an API response, an audit entry, a Kubernetes event or a
// metric label. The sentence a person reads is Result.ErrorMessage, which came
// from the catalog in errors.go.
func (r Result) Diagnostic() string { return r.diagnostic }
