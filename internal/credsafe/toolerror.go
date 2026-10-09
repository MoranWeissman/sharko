package credsafe

// toolerror.go — what Sharko is allowed to tell a language model when one of
// its own tools failed (B14).
//
// # The model provider is an outside party
//
// internal/ai runs a tool-calling loop. When a tool returns an error, the loop
// turns that error into a tool-result message, appends it to the conversation
// and sends the whole conversation to whichever provider the operator
// configured — Anthropic, OpenAI, Google, or a custom base URL. That is a
// third party, over the network, and it keeps what it is sent.
//
// The tools reach out to chart repositories and Git hosts, and those failures
// arrive as Go's own *url.Error, whose text keeps a token written in the
// username position of the address (measured in chartrepo.go). So
// `fmt.Sprintf("Error executing %s: %v", name, err)` was the operator's chart
// repository access token, posted to a model provider, from a single line.
//
// internal/ai had already been hardened for CONTENT: the tools run repository
// addresses through SafeRepoURL and statuses through SafeHealthStatus before
// putting them in an answer. Errors were the half nobody looked at, and there
// were twenty-six places to look — every `return "", err` in tools.go funnels
// into the ONE line that formats the result.
//
// # One boundary, not twenty-six call sites
//
// The fix is at that one line, for the same reason LogClass is applied at the
// log sink rather than at each slog call: a call-site fix protects the
// twenty-six that exist today and nothing written tomorrow. A twenty-seventh
// tool added next year is safe without its author knowing this file exists.
//
// # It still says a fetch failed, and roughly why
//
// A model told nothing would invent a reason. So the sentence says plainly
// that the step failed, and LogClass appends the classification — sentinel
// matches and Go TYPE NAMES, never err.Error(). `connection-refused
// chain=*url.Error>*net.OpError` is enough for the model to say "the chart
// repository did not answer" and not enough to say anything about the address.
//
// # There is no parameter for an error's words
//
// SafeToolFailure takes an error and nothing else. There is no string
// parameter into which a future caller could put err.Error() — passing one is
// a compile error, which is the same protection SafeOperationDetail and the
// connection gate rely on.

// ToolFailureMessage is the fixed sentence a model is told when a tool failed.
// It is a constant: the same words for every tool and every cause.
const ToolFailureMessage = "That step did not complete. Sharko is not passing on the underlying message, because a failure from a chart repository, a Git host or a credentials backend can quote an address that carries an access token."

// SafeToolFailure describes a failed tool call in words that are safe to send
// to a model provider: the fixed sentence, plus LogClass's type-derived
// classification so the model knows roughly what kind of failure it was.
//
// It never calls err.Error(), and it accepts nothing but an error.
func SafeToolFailure(err error) string {
	if err == nil {
		return ""
	}
	return ToolFailureMessage + " [" + LogClass(err) + "]"
}
