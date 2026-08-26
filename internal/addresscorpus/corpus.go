// Package addresscorpus reads the written-down list of network addresses and
// what the address rule says about each one.
//
// # What this package is for
//
// Sharko decides whether an operator-supplied network address may be used, and
// two separate places implement that decision: the Go classifier in
// internal/credsafe and the Helm chart under charts/sharko. When each side
// brings its own list of example addresses to its own tests, both sides can be
// green while they disagree, because each list only holds the shapes whoever
// wrote it had already thought of.
//
// So the list lives in one file, at testdata/address-rule-corpus.yaml, written
// from the rule rather than from either implementation, and both sides read it
// from here. This package is only the reader. It has no opinion about any
// address; it does not import internal/credsafe, and it must never grow a
// function that decides a verdict.
//
// # Why the reader is loud
//
// A list of expectations that quietly reads as empty turns every test built on
// it into a test that passes without looking at anything, which is the exact
// failure this file exists to prevent. So Load refuses to hand back anything
// at all when the file is missing, holds no rows, has a row with no reason
// written down, names the same address twice, or uses a verdict word that is
// not one of the two allowed. There is no "best effort" path and no partial
// result.
package addresscorpus

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"gopkg.in/yaml.v3"
)

// Verdict is what the rule says about one address. There are two values and
// there is deliberately no third: a row that does not say one of these two
// words is a broken row, not a new state.
type Verdict string

const (
	// Refused means the rule does not allow the address to be used, saved or
	// shown as it stands.
	Refused Verdict = "refused"

	// Accepted means the rule allows it.
	Accepted Verdict = "accepted"
)

// RelPath is where the list lives, relative to the repository root.
const RelPath = "testdata/address-rule-corpus.yaml"

// Row is one address, the verdict the rule gives it, and one sentence saying
// why the rule gives that verdict.
//
// Reason is not decoration. It is the only thing in the file that shows the
// row was worked out from the rule rather than copied from whatever the code
// happened to do on the day.
type Row struct {
	Address string  `yaml:"address"`
	Verdict Verdict `yaml:"verdict"`
	Reason  string  `yaml:"reason"`
}

// file is the shape of the YAML document.
type file struct {
	Rows []Row `yaml:"rows"`
}

// ErrNoRows is returned when the file was read but holds no rows. It is a
// named error because "the list is empty" is the failure that would otherwise
// look exactly like "everything passed".
var ErrNoRows = errors.New("the address corpus parsed to zero rows, so every test built on it would pass without checking anything")

// Path returns the absolute path of the list.
//
// It walks up from this source file until it finds the directory holding
// go.mod, so it works whatever directory `go test` was started from and
// whichever package is doing the reading.
func Path() (string, error) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return "", errors.New("cannot work out where this source file is, so the address corpus cannot be found")
	}
	dir := filepath.Dir(thisFile)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return filepath.Join(dir, filepath.FromSlash(RelPath)), nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("walked up from %s without finding go.mod, so the repository root is unknown", filepath.Dir(thisFile))
		}
		dir = parent
	}
}

// Load reads and checks the list.
//
// It returns an error, and no rows at all, for every one of these:
//
//   - the file cannot be found or cannot be read;
//   - it does not parse, or carries a key nobody declared;
//   - it holds no rows;
//   - a row has no reason written down;
//   - the same address appears on more than one row;
//   - a row's verdict is a word other than "refused" or "accepted".
func Load() ([]Row, error) {
	path, err := Path()
	if err != nil {
		return nil, err
	}
	body, err := os.ReadFile(path) // #nosec G304 -- the path is repository-controlled (Path() walks up from this source file to the go.mod root and joins the constant RelPath) and Load is called only from test code.
	if err != nil {
		return nil, fmt.Errorf("reading the address corpus at %s: %w", RelPath, err)
	}

	var doc file
	dec := yaml.NewDecoder(bytes.NewReader(body))
	// KnownFields makes a mistyped key ("adress:", "verdit:") a loud parse
	// failure instead of a row that silently loses a field.
	dec.KnownFields(true)
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("parsing the address corpus at %s: %w", RelPath, err)
	}

	if len(doc.Rows) == 0 {
		return nil, fmt.Errorf("%s: %w", RelPath, ErrNoRows)
	}

	seen := make(map[string]int, len(doc.Rows))
	for i, row := range doc.Rows {
		// The row number is what a person needs in order to find the row;
		// the address itself is quoted with %q so a control character in it
		// shows up as an escape rather than moving the cursor around.
		switch row.Verdict {
		case Refused, Accepted:
		default:
			return nil, fmt.Errorf("%s row %d (%q): verdict is %q, but the only two allowed words are %q and %q",
				RelPath, i+1, row.Address, string(row.Verdict), string(Refused), string(Accepted))
		}
		if strings.TrimSpace(row.Reason) == "" {
			return nil, fmt.Errorf("%s row %d (%q): no reason written down, so there is nothing showing this row was worked out from the rule",
				RelPath, i+1, row.Address)
		}
		if first, dup := seen[row.Address]; dup {
			return nil, fmt.Errorf("%s row %d (%q): this address is already on row %d, and one address cannot be given two verdicts",
				RelPath, i+1, row.Address, first)
		}
		seen[row.Address] = i + 1
	}

	return doc.Rows, nil
}

// Counts returns how many rows say refused and how many say accepted. It is
// here so a report or a test can state the shape of the list without every caller
// writing the same loop.
func Counts(rows []Row) (refused, accepted int) {
	for _, row := range rows {
		switch row.Verdict {
		case Refused:
			refused++
		case Accepted:
			accepted++
		}
	}
	return refused, accepted
}
