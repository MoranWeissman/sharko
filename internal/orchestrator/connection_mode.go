package orchestrator

import (
	"errors"
	"fmt"

	"github.com/MoranWeissman/sharko/internal/models"
)

// InvalidConnectionModeError is returned when a request's
// connection_managed_by value is not one of "", "sharko", or "user". It is
// a CALLER error — the API layer maps it to 400. Mirrors the
// *InvalidCredsSourceError pattern (typed error + Is-helper, no
// string-matching at the handler).
type InvalidConnectionModeError struct {
	Msg string
}

func (e *InvalidConnectionModeError) Error() string {
	if e == nil || e.Msg == "" {
		return "invalid connection_managed_by"
	}
	return e.Msg
}

// IsInvalidConnectionMode reports whether err is (or wraps) an
// *InvalidConnectionModeError. The API layer uses this to choose a 400.
func IsInvalidConnectionMode(err error) bool {
	var target *InvalidConnectionModeError
	return errors.As(err, &target)
}

// validateConnectionMode rejects unknown connection_managed_by values.
// "" (absent) and "sharko" mean Sharko-managed — today's behavior,
// byte-for-byte. "user" selects the self-managed connection mode
// (V2-cleanup-57.2). Anything else is a caller error rather than a silent
// fallback to Sharko-managed — silently defaulting on a typo would make
// Sharko take ownership of a connection the caller explicitly tried to
// keep.
func validateConnectionMode(value string) error {
	switch {
	case value == "",
		value == models.ConnectionManagedBySharko,
		value == models.ConnectionManagedByUser:
		return nil
	default:
		return &InvalidConnectionModeError{Msg: fmt.Sprintf(
			"unknown connection_managed_by %q (want %q or %q)",
			value, models.ConnectionManagedBySharko, models.ConnectionManagedByUser,
		)}
	}
}
