{{/*
Expand the name of the chart.
*/}}
{{- define "sharko.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "sharko.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "sharko.labels" -}}
helm.sh/chart: {{ include "sharko.name" . }}-{{ .Chart.Version | replace "+" "_" }}
{{ include "sharko.selectorLabels" . }}
app.kubernetes.io/version: {{ .Values.image.tag | default .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: sharko
{{- end }}

{{/*
Selector labels
*/}}
{{- define "sharko.selectorLabels" -}}
app.kubernetes.io/name: {{ include "sharko.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Service account name
*/}}
{{- define "sharko.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "sharko.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Secret name
*/}}
{{- define "sharko.secretName" -}}
{{- include "sharko.fullname" . }}
{{- end }}

{{/*
ArgoCD namespace for RBAC
*/}}
{{- define "sharko.argocdNamespace" -}}
{{- .Values.rbac.argocdNamespace | default "argocd" }}
{{- end }}

{{/*
sharko.unsupportedAddressMessage — the one sentence the chart says when it
will not install with an address it cannot prove is credential-free.

Why it does not claim a credential was found: the test underneath is
structural. It asks whether Sharko can read the address at all, and then
whether it carries user information before an "@" in the host part, a query
string, or a fragment — never whether the text sitting there looks secret. A
structural test is the only kind that does not fail on the first shape nobody
predicted, and the price is that an ordinary "?ref=main" is refused too. So
the sentence states the RULE, which is always true, rather than an accusation,
which would be a guess.

It carries nothing of the value: not the address, not a piece of it, not its
length, not a mask of it. A refusal travels further than the terminal it was
written for.
*/}}
{{- define "sharko.unsupportedAddressMessage" -}}
must be an address Sharko can read, with no user information before an "@" in the host part, no query string, and no fragment. Write it as https://host/path, or as host/path with no scheme, or as host:port. The chart writes this address into the pod as a plain value, which anyone who can read the Deployment can read, so it has to carry no credential. Credentials belong in a Secret: the Git and ArgoCD tokens in the connection Secret, the AI credential in ai.apiKey. Use a credential-free base URL.
{{- end }}

{{/*
sharko.classifyAddress — read one address the one way Sharko is willing to
read it, and say which of three things it is.

Call with the address itself. It prints exactly one of:

  unclassifiable       Sharko could not read it. Never means safe.
  carries-credential   read, and it has somewhere for a credential to sit.
  credential-free      read whole, with no user information, query or fragment.

internal/credsafe.ClassifyAddress makes this same decision for Go code. A
chart cannot call Go, so the rule is written out here a second time, and two
copies of a rule are two things that can drift apart.

# What holds the two together is a list, not a proof

The addresses live at testdata/address-rule-corpus.yaml, written from the
rule itself rather than from either copy.
tests/serverrender/bf13_chart_corpus_test.go asks this rule about every row
in that list and checks the answer against what the list says. Its
TestTheChartAndCredsafeReachTheSameVerdict then asks BOTH copies about that
list plus the hand-written table in bf11_addresses_test.go, and fails if they
answer differently about any address in either. Today they answer the same
about every one of them.

That is a test over two lists. An address in neither of them is not covered
by it, so the true thing to say is "the two copies agree about every address
that has been written down" — never "the two cannot differ" and never "a test
fails the day they do".

# The one difference that is known about, and why it is not a leak

Inside square brackets this rule checks that the text is made of the
characters an IPv6 address is made of. The Go copy hands the same text to
net/url, which checks that it really is one. So "[::1::2]:80" comes back
credential-free here and unclassifiable from internal/credsafe, and the chart
installs with it. Neither list holds a shape like that, which is why no test
is red.

Nothing in that difference can carry a credential, and the reason is
structural rather than lucky: the only branch below that sets
"credential-free" requires no user information, no query and no fragment, and
user information is taken off the authority BEFORE the brackets are read at
all. So an address with a credential written inside the brackets is refused
here too. What can get past this rule and not past the Go one is a host that
is not a real address — never a secret.

# The zero value is the unsafe one

$verdict starts at "unclassifiable" and is only ever moved to
"credential-free" by a branch that has proved the whole address. There is no
path that reaches the end by not having found anything wrong. That is the
difference between "this is safe" and "nothing identified it as unsafe", and
the second one is what leaked.

# A grammar with exactly two shapes

The way an address was read here used to end in a branch that accepted rather
than checked: anything that was not written with a "<scheme>://" and did not
already start with "//" was searched for an "@" in whatever text sat before
the first "/", and accepted when it found none. So an address whose scheme was
MISTYPED had the broken piece of scheme read as its host, and the whole of the
operator's user information sat in what this rule called the path, where an
"@" is ordinary. The address came back credential-free and went into the
Deployment exactly as written.

There is no one way to mistype a scheme, so there is no list of mistyped
schemes here and there must never be one. What replaced that branch is two
accepted shapes and a set of structural checks every address is put through,
whichever shape it was written as:

  Form 1 — a scheme that starts at the very first byte and is followed by
           "://". A scheme is one letter followed by any number of letters,
           digits, "+", "-" or ".". That is SYNTAX, not a list of protocols:
           oci://, git+ssh://, file:// and ftp:// are all read on structure
           alone, because which protocols a Sharko feature actually speaks is
           that feature's business and not this rule's.

  Form 2 — no scheme at all: what is left has to become a valid authority,
           plus an optional path, when a single "//" is put in front of it.

An address already written with its own leading "//" is form 2 as it stands.
A second "//" would turn "////user:pw@host" into a path and make the user
information vanish, so it is never added twice.

An address that is neither of those two shapes is read the way form 2 is read,
with a single "//" put in front of it, and is then held to every check below.
Nothing is accepted for having got that far. Most mistyped schemes fail on
their host, because what is left of the scheme still carries its colon:
"https:/git.example/o/r" reads as authority "https:", which is a host with a
port written and left empty, and an empty port is a refusal.

The one where the COLON is left out does not fail that way, and for a while it
was accepted. "https//git.example/o/r" leaves "https", which is a perfectly
good host name, so the whole of the user information sat in the path. It is
refused now by a rule about the SHAPE — a second "//" straight after the
authority, in an address that had no "//" of its own — rather than by anything
that recognises the text of a scheme.

# What has to hold for either form

All of these, or the address is refused:

  * nothing at either end but the address — no leading or trailing
    whitespace — and no raw control character anywhere in it;
  * no percent escape that is not a well-formed one;
  * an authority to read at all;
  * a valid, non-empty host, made only of characters a host may be made of;
  * a port, if a ":" was written, that is not empty, is all digits, and is
    not above 65535;
  * inside brackets, something shaped like an IPv6 address, with a zone
    identifier written the one way it may be written, "%25" and a name;
  * in an address that had no scheme and no "//" of its own, no second "//"
    straight after the authority.

# Percent signs are judged by where they sit, never by what they spell

A "%" in an ordinary path is path data and stays there: "%20" between two
words is a space somebody wrote down. A "%" in an UNBRACKETED host is how
user information gets written so that a reader looking for a literal "@"
never finds one — "user%3Apw%40git.example" is one legal-looking hostname and
was accepted here for exactly that reason. In a host, a percent escape may
only stand for a byte outside ASCII, so an escape whose first digit is 0 to 7
is refused. Inside brackets a "%" is how an IPv6 zone identifier has to be
written and one is expected.

# Read it whole, THEN look for a credential

Every structural check runs BEFORE the question "is there a credential in
it". So "carries-credential" is only ever the answer for an address that is
otherwise entirely readable, and "git@github.com:org/repo.git" — whose ":org"
is not a port — comes back unclassifiable rather than carries-credential,
which is the same answer the Go copy gives.

# Two traps, both from the way net/url reads an address, both handled

The fragment comes off the whole address FIRST, before the scheme, because
that is the order the Go copy reads it in and "git.example#x" has to reach
the same answer both ways. And a bare "?" at the end is a query as far as
that reading is concerned, while a bare trailing "#" is an EMPTY fragment and
is not. Both of those are matched here on purpose.
*/}}
{{- define "sharko.classifyAddress" -}}
{{- $raw := . | toString -}}
{{- $verdict := "unclassifiable" -}}
{{- $readable := true -}}

{{- /* Nothing at all, whitespace at either end, or a raw control character
     anywhere — including the C1 range, which is where a control character
     hides when somebody has already checked for the obvious ones. A raw
     newline is the shape that gets through when a reader stops at the first
     line and treats what follows as a separate, safe value. */ -}}
{{- if or (eq $raw "") (ne (trim $raw) $raw) -}}
{{- $readable = false -}}
{{- else if regexMatch `[\x{0000}-\x{001f}\x{007f}-\x{009f}]` $raw -}}
{{- $readable = false -}}
{{- end -}}

{{- /* The fragment comes off the whole address before anything else. */ -}}
{{- $body := $raw -}}
{{- $fragment := "" -}}
{{- if and $readable (contains "#" $raw) -}}
{{- $halves := splitn "#" 2 $raw -}}
{{- $body = $halves._0 -}}
{{- $fragment = $halves._1 -}}
{{- end -}}

{{- /* One of the two shapes, and nothing else. Whichever it is, what is left
     starts with "//". */ -}}
{{- $rest := "" -}}
{{- $hasScheme := false -}}
{{- $addedSlashes := false -}}
{{- if $readable -}}
{{- if regexMatch `^[A-Za-z][A-Za-z0-9+.\-]*://` $body -}}
{{- $hasScheme = true -}}
{{- $rest = regexReplaceAll `^[A-Za-z][A-Za-z0-9+.\-]*:` $body "" -}}
{{- else if hasPrefix "//" $body -}}
{{- $rest = $body -}}
{{- else -}}
{{- $addedSlashes = true -}}
{{- $rest = printf "//%s" $body -}}
{{- end -}}
{{- end -}}

{{- /* The query. Any "?" at all is one, whether or not anything follows it. */ -}}
{{- $hasQuery := false -}}
{{- if and $readable (contains "?" $rest) -}}
{{- $hasQuery = true -}}
{{- $rest = (splitn "?" 2 $rest)._0 -}}
{{- end -}}

{{- /* A percent escape that is not one. This is checked over the host and the
     path, and over the fragment, and deliberately NOT over the query, which
     is the same set of places the Go copy unescapes. */ -}}
{{- if and $readable (or (regexMatch `%($|[^0-9A-Fa-f]|[0-9A-Fa-f]($|[^0-9A-Fa-f]))` $rest) (regexMatch `%($|[^0-9A-Fa-f]|[0-9A-Fa-f]($|[^0-9A-Fa-f]))` $fragment)) -}}
{{- $readable = false -}}
{{- end -}}

{{- /* The authority: what sits between the "//" and the first "/" after it.
     With no scheme in front, three or more slashes is a path and there is no
     authority to read, so "////user:pw@host" has no host and is refused
     rather than having its user information read as path text. */ -}}
{{- $authority := "" -}}
{{- if $readable -}}
{{- if or $hasScheme (not (hasPrefix "///" $rest)) -}}
{{- $authority = (splitn "/" 2 (substr 2 (len $rest) $rest))._0 -}}
{{- else -}}
{{- $readable = false -}}
{{- end -}}
{{- end -}}

{{- /* A second "//" straight after the authority, in the one case where the
     first "//" was this rule's own doing.

     An address written with no scheme is a host, an optional port and an
     optional path, and a path in it never opens a second authority. When a
     second "//" is there, what was written is a scheme with its colon left
     out — "https//git.example/o/r" — and the first segment is that scheme
     rather than a host anybody meant to reach. Every other way of mistyping a
     scheme is turned away further down, because what is left of the scheme
     still carries its colon and reads as a host with an empty port. This one
     is not: "https" on its own is a perfectly good host name, so the whole of
     the user information sits in what this rule would call the path, where an
     "@" is ordinary, and the address came back credential-free and went into
     the Deployment exactly as written.

     The other two shapes are deliberately left alone. There the "//" that
     opens the authority was written by the operator, so where the authority
     ends is not in doubt and a doubled slash after it is ordinary path
     text. */ -}}
{{- if and $readable $addedSlashes -}}
{{- if hasPrefix (printf "//%s//" $authority) $rest -}}
{{- $readable = false -}}
{{- end -}}
{{- end -}}

{{- /* User information is whatever sits before the LAST "@" in the authority,
     so "a@b@git.example" is user information "a@b" and host "git.example".
     It is noted here and answered at the very end: an address that cannot be
     read whole is unclassifiable even when it also carries a credential. */ -}}
{{- $hasUserinfo := false -}}
{{- $hostport := $authority -}}
{{- if and $readable (contains "@" $authority) -}}
{{- $hasUserinfo = true -}}
{{- $hostport = regexReplaceAll `^.*@` $authority "" -}}
{{- if not (regexMatch `^[A-Za-z0-9\-._:~!$&'()*+,;=%@]*$` (regexReplaceAll `@[^@]*$` $authority "")) -}}
{{- $readable = false -}}
{{- end -}}
{{- end -}}

{{- /* The host, and the port if one was written. */ -}}
{{- $hostname := "" -}}
{{- $portWritten := false -}}
{{- $port := "" -}}
{{- if $readable -}}
{{- if hasPrefix "[" $hostport -}}
{{- if not (contains "]" $hostport) -}}
{{- $readable = false -}}
{{- else -}}
{{- $hostname = trimPrefix "[" (regexReplaceAll `\][^\]]*$` $hostport "") -}}
{{- $tail := regexReplaceAll `^.*\]` $hostport "" -}}
{{- if ne $tail "" -}}
{{- if hasPrefix ":" $tail -}}
{{- $portWritten = true -}}
{{- $port = substr 1 (len $tail) $tail -}}
{{- else -}}
{{- $readable = false -}}
{{- end -}}
{{- end -}}
{{- /* Inside brackets there has to be something shaped like an IPv6 address,
     and a zone identifier is written "%25" and a name — the one place in an
     address where a "%" belongs in the host. */ -}}
{{- if and $readable (not (regexMatch `^[0-9A-Fa-f:.]+(%25[0-9A-Za-z._~-]+)?$` $hostname)) -}}
{{- $readable = false -}}
{{- end -}}
{{- end -}}
{{- else -}}
{{- if contains ":" $hostport -}}
{{- $portWritten = true -}}
{{- $port = regexReplaceAll `^.*:` $hostport "" -}}
{{- $hostname = regexReplaceAll `:[^:]*$` $hostport "" -}}
{{- else -}}
{{- $hostname = $hostport -}}
{{- end -}}
{{- /* The characters a host outside brackets may be made of. A "%" is allowed
     through here and judged just below by what it escapes. */ -}}
{{- if not (regexMatch `^[A-Za-z0-9._~!$&'()*+,;=%"<>\]\x{00a0}-\x{10ffff}-]+$` $hostname) -}}
{{- $readable = false -}}
{{- end -}}
{{- /* In a host a percent escape may only stand for a byte outside ASCII, so
     an escape whose first digit is 0 to 7 cannot be one. That is what
     "user%3Apw%40git.example" is: user information written so that nothing
     looking for a literal "@" will find one. */ -}}
{{- if and $readable (regexMatch `%[0-7]` $hostname) -}}
{{- $readable = false -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{- /* A port that was written has to be a number, and a number that exists.
     Leading zeros come off first so a long run of them is not mistaken for a
     port too big to be one. */ -}}
{{- if and $readable $portWritten -}}
{{- $digits := regexReplaceAll `^0+` $port "" -}}
{{- /* The empty case is named on its own even though the all-digits test
     below would turn it away too. "host:" is a port somebody meant to write
     and did not finish, and it reads as a refusal here rather than as an
     accident of a character class — which is what stops the next person
     tidying the two into one test that only looks at the digits. */ -}}
{{- if eq $port "" -}}
{{- $readable = false -}}
{{- else if not (regexMatch `^[0-9]+$` $port) -}}
{{- $readable = false -}}
{{- else if gt (len $digits) 5 -}}
{{- $readable = false -}}
{{- else if gt (atoi $digits) 65535 -}}
{{- $readable = false -}}
{{- end -}}
{{- end -}}

{{- /* The address has now been read whole. Only at this point is it worth
     asking whether there is anywhere in it for a credential to sit. An empty
     user information section counts: "https://@git.example/o/r" has one, and
     the rule refuses the section being there at all. */ -}}
{{- if $readable -}}
{{- if or $hasUserinfo $hasQuery (ne $fragment "") -}}
{{- $verdict = "carries-credential" -}}
{{- else -}}
{{- $verdict = "credential-free" -}}
{{- end -}}
{{- end -}}
{{- $verdict -}}
{{- end }}

{{/*
sharko.requireCredentialFreeAddress — refuse to render unless an operator-set
address has been read whole and proved credential-free.

Call with a list: (list "<values path>" <the value>). The values path is a
name a programmer wrote in this repository, never anything derived from the
value.

An empty value means the operator has not set this field. That is not an
address at all, so there is nothing to classify and nothing to refuse; the
caller decides what an unset field means, exactly as internal/credsafe leaves
the empty case to its own callers.

Everything else must come back credential-free. Not "not carries-credential" —
credential-free, by name. An address Sharko could not read is refused here for
the same reason a credential-bearing one is: this value is about to be written
into the pod in the clear, and "I could not tell" is not permission.
*/}}
{{- define "sharko.requireCredentialFreeAddress" -}}
{{- $field := index . 0 -}}
{{- $raw := index . 1 | toString -}}
{{- if ne $raw "" -}}
{{- if ne (include "sharko.classifyAddress" $raw) "credential-free" -}}
{{- fail (printf "%s %s" $field (include "sharko.unsupportedAddressMessage" $)) -}}
{{- end -}}
{{- end -}}
{{- end }}

{{/*
sharko.validateAddresses — every operator-set address the chart writes into
the Pod, checked in one place so a new one is added next to the others.

Every value listed below is declared in values.yaml as NON-SECRET, and the
Deployment writes them into the Pod as ordinary environment values that
anyone with read access to the Deployment can see. That claim is only true
if the chart refuses the credential-bearing forms, so it refuses them here.
TestTheRuleChecksExactlyTheAddressesClaimedHere, in
tests/serverrender/bf11_address_coverage_test.go, reads these lines back out
of this file, so an address added here and not there — or there and not here
— turns that test red.
*/}}
{{- define "sharko.validateAddresses" -}}
{{- $connection := .Values.connection | default dict -}}
{{- $git := $connection.git | default dict -}}
{{- $argocd := $connection.argocd | default dict -}}
{{- $ai := .Values.ai | default dict -}}
{{- $ollama := $ai.ollama | default dict -}}
{{- include "sharko.requireCredentialFreeAddress" (list "connection.git.repoURL" ($git.repoURL | default "")) -}}
{{- include "sharko.requireCredentialFreeAddress" (list "connection.argocd.serverURL" ($argocd.serverURL | default "")) -}}
{{- include "sharko.requireCredentialFreeAddress" (list "ai.baseURL" ($ai.baseURL | default "")) -}}
{{- include "sharko.requireCredentialFreeAddress" (list "ai.ollama.url" ($ollama.url | default "")) -}}
{{- end }}

{{/*
sharko.bootstrapPasswordKey — the key in the chart's own Secret that holds
the PLAINTEXT bootstrap admin password, for the path where the operator set
bootstrapAdmin.password inline.

It is deliberately not admin.password. That key holds a bcrypt hash, and the
Pod needs the plaintext: internal/auth/store.go SeedBootstrapAdminFromEnv
reads SHARKO_BOOTSTRAP_ADMIN_PASSWORD and hashes it itself.

Named once here because two files have to agree on it — secret.yaml writes
it and deployment.yaml references it, and a reference to a key that is not
there stops the Pod from starting.
*/}}
{{- define "sharko.bootstrapPasswordKey" -}}
admin.bootstrapPassword
{{- end }}
