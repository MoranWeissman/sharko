package api

// fanout_surfaces_test.go — R2-9.
//
// Four different things tell somebody how a fan-out operation went:
//
//	the response body      the aggregate counts a script reads
//	the audit trail        what an operator finds afterwards
//	the printed summary    the line a person actually reads
//	the exit code          what a script branches on
//
// Before this story they did not agree. `sharko add-clusters` printed "done"
// and exited 0 for a batch in which every single cluster failed. The adopt
// endpoint answered 200 with no aggregate counts at all, so a client had no
// way to tell an adoption that worked from one that failed outright without
// walking results[] and classifying each status itself.
//
// This file is the table the product owner asked for: the seven outcome
// shapes, each one driven through the REAL production function behind every
// one of the four surfaces, with the agreement between them asserted rather
// than assumed. The shapes come from internal/fanout so the same seven drive
// the CLI's own tests.

import (
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/fanout"
	"github.com/MoranWeissman/sharko/internal/fanout/fanouttest"
	"github.com/MoranWeissman/sharko/internal/lifecycleevents"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
)

// batchResultFor builds the BatchResult the orchestrator would return for a
// shape, with the older counters set exactly the way RegisterClusterBatch
// sets them: `failed` counts every cluster that did not FULLY succeed.
func batchResultFor(s fanouttest.Shape) *orchestrator.BatchResult {
	br := &orchestrator.BatchResult{}
	for _, status := range s.Statuses() {
		br.Results = append(br.Results, orchestrator.RegisterClusterResult{Status: status})
		br.Total++
		switch status {
		case "partial":
			br.Partial++
			br.Failed++
		case "failed":
			br.Failed++
		default:
			br.Succeeded++
		}
	}
	br.Summarize()
	return br
}

func adoptResultFor(s fanouttest.Shape) *orchestrator.AdoptClustersResult {
	res := &orchestrator.AdoptClustersResult{}
	for _, status := range s.Statuses() {
		res.Results = append(res.Results, orchestrator.AdoptClusterResult{Status: status})
	}
	res.Summarize()
	return res
}

// TestFanOutSurfaces_AllFourAgree is items 1-8 of the break list in one
// table: the seven shapes, every surface, checked against each other.
func TestFanOutSurfaces_AllFourAgree(t *testing.T) {
	for _, shape := range fanouttest.Ruled() {
		shape := shape
		t.Run(shape.What, func(t *testing.T) {
			br := batchResultFor(shape)
			ar := adoptResultFor(shape)

			// ── Surface 1: the aggregate counts in the response body ─────
			for name, got := range map[string]fanout.Outcome{
				"POST /clusters/batch": br.Outcome,
				"POST /clusters/adopt": ar.Outcome,
			} {
				if got.Completed != shape.Completed ||
					got.PartlyCompleted != shape.PartlyCompleted ||
					got.Failed != shape.Failed ||
					got.Unrecognized != shape.Unrecognized {
					t.Errorf("%s answered counts %+v for %s — the body must say what really happened",
						name, got, shape.What)
				}
				if got.Total != shape.Completed+shape.PartlyCompleted+shape.Failed+shape.Unrecognized {
					t.Errorf("%s: total %d does not match the buckets", name, got.Total)
				}
			}

			// The two endpoints must not describe the same outcome
			// differently. They used to: one carried counts, the other
			// carried none at all.
			if br.Outcome != ar.Outcome {
				t.Errorf("batch says %+v and adopt says %+v about the same outcome", br.Outcome, ar.Outcome)
			}
			outcome := br.Outcome

			// ── Surface 2: the audit trail ───────────────────────────────
			batchResult, batchChanges := batchAuditOutcome(br)
			adoptResult, adoptChanges := adoptAuditOutcome(ar, false)
			if batchResult != adoptResult || batchChanges != adoptChanges {
				t.Errorf("the two handlers write different audit lines for %s: "+
					"batch %q·%q, adopt %q·%q", shape.What, batchResult, batchChanges, adoptResult, adoptChanges)
			}
			for _, e := range []audit.Entry{
				{Event: lifecycleevents.ClusterRegistered, Level: levelFromResult(batchResult), Result: batchResult, Changes: batchChanges},
				{Event: lifecycleevents.ClusterAdopted, Level: levelFromResult(adoptResult), Result: adoptResult, Changes: adoptChanges},
			} {
				if bad := validateLifecycleEntry(e); len(bad) > 0 {
					t.Errorf("%s produces a self-contradicting audit entry: %v", shape.What, bad)
				}
			}

			// ── Surface 3: the summary a person reads ────────────────────
			summary := outcome.SummaryLine("Cluster registration")
			warning := outcome.ReviewWarning()

			// ── Surface 4: the exit code ─────────────────────────────────
			exitErr := outcome.ExitError("Cluster registration")

			// ── Now: do they agree? ──────────────────────────────────────
			everythingCompleted := shape.Completed > 0 &&
				shape.PartlyCompleted == 0 && shape.Failed == 0 && shape.Unrecognized == 0

			if everythingCompleted {
				if batchResult != "success" {
					t.Errorf("everything completed, audit says %q", batchResult)
				}
				if batchChanges != audit.ChangesApplied {
					t.Errorf("everything completed, audit changes say %q", batchChanges)
				}
				if exitErr != nil {
					t.Errorf("everything completed, yet the command exits non-zero: %v", exitErr)
				}
				if !strings.Contains(summary, "finished for every cluster") {
					t.Errorf("everything completed, yet the summary is %q", summary)
				}
				if warning != "" {
					t.Errorf("everything completed, yet a review warning was printed: %q", warning)
				}
				return
			}

			// Something did not finish. EVERY surface has to say so.
			if batchResult == "success" {
				t.Errorf("%s: the audit trail claims success for work that did not complete", shape.What)
			}
			if exitErr == nil {
				t.Errorf("%s: the command exits 0. A script is told everything worked.", shape.What)
			}
			if !strings.Contains(summary, "did NOT finish for every cluster") {
				t.Errorf("%s: the summary does not say the run did not finish: %q", shape.What, summary)
			}
			if strings.Contains(strings.ToLower(summary), "done") {
				t.Errorf("%s: the summary says \"done\": %q", shape.What, summary)
			}

			// Nothing landed anywhere is the ONE case that may say so.
			nothingLanded := shape.Completed == 0 && shape.PartlyCompleted == 0 && shape.Unrecognized == 0
			if nothingLanded {
				if batchResult != "failure" || batchChanges != audit.ChangesNone {
					t.Errorf("%s: nothing landed, audit says %q·%q", shape.What, batchResult, batchChanges)
				}
			} else if batchChanges == audit.ChangesNone {
				t.Errorf("%s: real work landed or may have landed, and the audit trail says "+
					"nothing changed", shape.What)
			}

			// A part-way cluster: "may have been applied", never either
			// certainty, and a warning that says changes may be left behind.
			if shape.PartlyCompleted > 0 {
				if batchChanges != audit.ChangesMayBeApplied {
					t.Errorf("%s: a cluster stopped part-way, so the audit trail must say the "+
						"changes MAY have been applied, got %q", shape.What, batchChanges)
				}
				if warning == "" {
					t.Errorf("%s: a cluster stopped part-way and nothing tells the operator "+
						"real changes may be left behind", shape.What)
				}
			} else if !nothingLanded && batchChanges != audit.ChangesApplied {
				// Some finished, some failed, none part-way: at least one
				// cluster completed every step, so changes definitely landed.
				t.Errorf("%s: at least one cluster completed every step, so changes were "+
					"definitely applied, yet the audit trail says %q", shape.What, batchChanges)
			}
		})
	}
}

// TestFanOutSurfaces_HTTPStatusNeverMoves is the hard constraint written down
// as a test. The product owner ruled explicitly that no status code changes in
// this release: a 200 may truthfully carry an all-failed batch as long as the
// body says so, and that is exactly what R2-9 did instead.
//
// Both rules are written out here independently of the production code.
func TestFanOutSurfaces_HTTPStatusNeverMoves(t *testing.T) {
	for _, shape := range fanouttest.Ruled() {
		// Batch: 207 whenever any cluster did not FULLY succeed.
		wantBatch := http.StatusOK
		if shape.PartlyCompleted+shape.Failed > 0 {
			wantBatch = http.StatusMultiStatus
		}
		if got := batchHTTPStatus(batchResultFor(shape)); got != wantBatch {
			t.Errorf("%s → batch HTTP %d, want %d. Status codes were NOT this story's to change.",
				shape.What, got, wantBatch)
		}

		// Adopt: 207 only when at least one failed and at least one did not.
		wantAdopt := http.StatusOK
		if shape.Failed > 0 && shape.Completed+shape.PartlyCompleted+shape.Unrecognized > 0 {
			wantAdopt = http.StatusMultiStatus
		}
		if got := adoptHTTPStatus(adoptResultFor(shape)); got != wantAdopt {
			t.Errorf("%s → adopt HTTP %d, want %d. Status codes were NOT this story's to change.",
				shape.What, got, wantAdopt)
		}
	}
}

// TestFanOutSurfaces_A200CanCarryAnAllFailedBatch is the ruling's own
// sentence turned into a test: the status stays, and the body is what has to
// make the outcome explicit. If the body ever stopped carrying accurate
// counts, HTTP 200 on an all-failed adoption would be a plain lie again.
func TestFanOutSurfaces_A200CanCarryAnAllFailedBatch(t *testing.T) {
	allFailed := fanouttest.Shape{What: "every cluster failed", Failed: 3}
	ar := adoptResultFor(allFailed)

	if got := adoptHTTPStatus(ar); got != http.StatusOK {
		t.Fatalf("adopt answered %d for an all-failed adoption; it has always answered 200 "+
			"and changing that is a major version bump", got)
	}
	if ar.Outcome.Completed != 0 || ar.Outcome.Failed != 3 || ar.Outcome.Total != 3 {
		t.Fatalf("the 200 carries counts %+v — the body is the ONLY thing saying this failed, "+
			"so it has to be right", ar.Outcome)
	}
	if ar.Outcome.EverythingCompleted() {
		t.Fatal("an all-failed adoption reports everything completed")
	}
}

// TestFanOutBody_CarriesNoTopLevelSuccessClaim. The ruling: never place a
// top-level success or completion statement beside an all-failed or partial
// result. Neither response body has such a field — this fails if one is
// added, because a new top-level string next to accurate counts is exactly
// the thing that would contradict them.
func TestFanOutBody_CarriesNoTopLevelSuccessClaim(t *testing.T) {
	shape := fanouttest.Shape{What: "all failed", Failed: 2}

	for name, body := range map[string]any{
		"BatchResult":         batchResultFor(shape),
		"AdoptClustersResult": adoptResultFor(shape),
	} {
		for _, field := range topLevelStringFields(body) {
			t.Errorf("%s has a top-level string field %q. Beside an all-failed result, any "+
				"top-level message, status or completion wording contradicts the counts "+
				"next to it — the ruling forbids exactly that. If the field is genuinely "+
				"needed, it must never carry completion wording for a run that did not complete.",
				name, field)
		}
	}
}

// topLevelStringFields names the exported string-typed fields at the top
// level of a response body. Reflection rather than a hand-written list, so a
// field added tomorrow is caught without anybody remembering to look.
func topLevelStringFields(v any) []string {
	rt := reflect.TypeOf(v)
	for rt.Kind() == reflect.Pointer {
		rt = rt.Elem()
	}
	if rt.Kind() != reflect.Struct {
		return nil
	}
	var out []string
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		if !f.IsExported() || f.Type.Kind() != reflect.String {
			continue
		}
		name := f.Name
		if tag := f.Tag.Get("json"); tag != "" && tag != "-" {
			name = strings.Split(tag, ",")[0]
		}
		out = append(out, name)
	}
	return out
}
