// Package fanouttest holds the outcome shapes every surface is tested over.
//
// A fan-out operation's result has four surfaces — the response body, the
// audit trail, the summary a person reads and the exit code a script branches
// on — and they live in three different Go packages. They agree only if they
// are all driven over the SAME shapes, so the shapes live here rather than in
// any one package's test file, where the other two could not see them.
//
// This package is test support. Nothing in the shipped server or CLI imports
// it.
package fanouttest

import "github.com/MoranWeissman/sharko/internal/fanout"

// Shape is one fan-out run's worth of per-item answers.
type Shape struct {
	What            string
	Completed       int
	PartlyCompleted int
	Failed          int
	Unrecognized    int
}

// Statuses is the per-item status list this shape comes back as, using the
// orchestrator's own status strings.
func (s Shape) Statuses() []string {
	var out []string
	for i := 0; i < s.Completed; i++ {
		out = append(out, fanout.StatusCompleted)
	}
	for i := 0; i < s.PartlyCompleted; i++ {
		out = append(out, fanout.StatusPartlyCompleted)
	}
	for i := 0; i < s.Failed; i++ {
		out = append(out, fanout.StatusFailed)
	}
	for i := 0; i < s.Unrecognized; i++ {
		// "skipped" is declared on AdoptClusterResult.Status and no code
		// path produces it. It stands in for ANY status a future change
		// invents — the point being that an answer nobody recognises must
		// never be read as a clean completion.
		out = append(out, "skipped")
	}
	return out
}

// Total is how many items this shape has.
func (s Shape) Total() int {
	return s.Completed + s.PartlyCompleted + s.Failed + s.Unrecognized
}

// EverythingCompleted says whether this shape is the one clean case, written
// out independently of the production code so a test comparing against it is
// not comparing a constant with itself.
func (s Shape) EverythingCompleted() bool {
	return s.Completed > 0 && s.PartlyCompleted == 0 && s.Failed == 0 && s.Unrecognized == 0
}

// Ruled is the product owner's list of outcome shapes, all seven, by name.
func Ruled() []Shape {
	return []Shape{
		{What: "1. all completed", Completed: 3},
		{What: "2. all failed", Failed: 3},
		{What: "3. all partly completed", PartlyCompleted: 3},
		{What: "4. completed plus failed", Completed: 2, Failed: 1},
		{What: "5. completed plus partly completed", Completed: 2, PartlyCompleted: 1},
		{What: "6. partly completed plus failed", PartlyCompleted: 2, Failed: 1},
		{What: "7. all three outcomes together", Completed: 1, PartlyCompleted: 1, Failed: 1},
	}
}

// EverySmall walks every combination of the four buckets up to three items,
// so the awkward ones are covered whether or not anybody thought of them.
func EverySmall() []Shape {
	var out []Shape
	for total := 1; total <= 3; total++ {
		for c := 0; c <= total; c++ {
			for p := 0; p <= total-c; p++ {
				for f := 0; f <= total-c-p; f++ {
					out = append(out, Shape{
						What:            "counts",
						Completed:       c,
						PartlyCompleted: p,
						Failed:          f,
						Unrecognized:    total - c - p - f,
					})
				}
			}
		}
	}
	return out
}
