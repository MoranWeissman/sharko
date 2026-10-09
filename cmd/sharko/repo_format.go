package main

import (
	"encoding/json"
	"fmt"
)

// repoFormatStatus mirrors orchestrator.MigrationStatusResult — the fields
// the CLI cares about from GET /api/v1/migration/status.
type repoFormatStatus struct {
	// Format is "v3", "v4", "empty", or "mixed" (both layouts present at
	// once).
	Format  string `json:"format"`
	Message string `json:"message"`
}

// repoFormat calls GET /api/v1/migration/status and reports which
// data-file format the connected repo uses. It is the one shared check
// commands that write the v3-shaped catalog/values files (upgrade-addon,
// upgrade-addons) use to decide whether that write is even going to land
// anywhere a v4 repo reads from.
//
// Any failure to reach the endpoint — network error, non-200, bad JSON —
// is reported back as an error. Callers are expected to fail OPEN on that
// error: treat it the same as "v3", today's behavior, rather than blocking
// a command over a status probe that itself could not be answered.
func repoFormat() (repoFormatStatus, error) {
	respBody, status, err := apiGet("/api/v1/migration/status")
	if err != nil {
		return repoFormatStatus{}, err
	}
	if status != 200 {
		return repoFormatStatus{}, fmt.Errorf("checking repo format: %w", printAPIError(respBody, status))
	}
	var s repoFormatStatus
	if err := json.Unmarshal(respBody, &s); err != nil {
		return repoFormatStatus{}, fmt.Errorf("invalid repo format response: %w", err)
	}
	return s, nil
}
