package api

import (
	"context"
	"time"
)

// joinWriteTimeout is how long an adopt, un-adopt or take over may run after
// the person who asked has gone. It is far longer than any of them takes (a
// Git write, a pull request, a mark), and short enough that a stuck Git host
// cannot hold the work for ever.
const joinWriteTimeout = 2 * time.Minute

// detachedWriteContext returns a context that keeps everything the request
// carries (who asked, the audit slot, the logger) but is NOT cancelled when
// the person closes the tab or reloads the page.
//
// Adopt, un-adopt and take over are made of several steps that only make
// sense together: open the pull request, put the waiting mark on the
// connection, record the pull request so the merge finishes the job. When the
// request context was used for all of them, closing the browser during the
// few seconds the page says "Adopting..." cut the steps in two. The pull
// request was opened, but the mark and the record were refused with
// "context canceled", so nothing ever finished it (walk 9, finding 1). With
// this context the steps run to the end whether or not anyone is still
// watching. The audit entry is written from the same request, so the log
// stays honest.
func detachedWriteContext(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(parent), joinWriteTimeout)
}
