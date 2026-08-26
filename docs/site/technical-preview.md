# Technical preview — read this before you point Sharko at a cluster

Sharko is published as an **open-source technical preview**. It is **not
production ready**, and this page exists so that you can decide for
yourself, from facts, rather than from a promise.

Here is the difference, in the plainest words we have:

- A **technical preview** is safe enough to read, install somewhere that
  does not matter, experiment with, and send us feedback about.
- A **production-ready release** would be one where we tell you to trust
  it with the credentials to your real clusters. Sharko is not there. Getting
  there needs more real operational use, and a security assessment by
  someone outside this project.

Nobody outside this project has assessed Sharko's security. That is the
single most important sentence on this page.

The eight sections below are the things we think you need to know. Each one
has its own heading so you can find it and read it on its own.

---

## 1. What we know about credential leaks, wrong permissions, and Sharko saying something finished when it did not

**The honest claim is "no known", not "none".**

As of **20 August 2026**, at commit **`6c0e8410`**, there are no *known*
credential leaks, no *known* ways to get past Sharko's permission checks,
and no *known* places where Sharko reports success for work that failed.

That is a much smaller claim than it sounds, and here is why.

### What we found and fixed just before writing this

The round of work that produced this page went looking for these problems
on purpose, and found a long series of them. Not one of them was found by a
test going red. Every one was found by a person reading the code and asking
"what exactly travels out of here?".

**A Git repository address usually has the password inside it.** People
write repository addresses like
`https://x-access-token:SECRET@github.example/org/repo.git`. The token is
part of the address. Anything that prints the address prints the token, and
at the place where the code does it, it looks completely innocent — it looks
like helpfully saying *which* repository had a problem.

That address was being handed out by:

- 64 different error replies, from endpoints at every permission level,
  including read-only ones
- a status call anybody could make during first-time setup
- the cluster comparison page, on an ordinary reply where nothing had gone
  wrong
- the observability page, the dashboard, and the addon detail page, where it
  was a link you could click
- the addon list, the catalog pages, and the catalog sources page
- the context handed to the AI assistant, so it could come back out of the
  assistant's answers too
- the server log

**Sharko also said things that were not true.** It promised to rebuild a
cluster connection on paths where it never would. It told you it would not
create a connection for clusters where it does in fact create one. When a
batch of clusters all got halfway and stopped, it wrote "nothing changed"
into the activity history. When an adoption of several clusters all got
halfway and stopped, it wrote "success, changes applied". Five commands
printed "done" and exited with a success code for work that had failed.

**Three numbers on the metrics page published a zero** when nothing was
measuring them at all, so a dashboard showed a confident, wrong number
instead of a gap.

All of those are fixed at the commit named above, and each fix is held in
place by a test that fails if it comes back.

### How much the tests that claim to cover everything have themselves been tested

Some of Sharko's tests do not check one example. They claim to have looked at
everything: every endpoint, every message, every file in a tree, every entry
in a list. A test like that is the reason we can say a whole class of problem
is gone rather than "gone in the case we tried". So it matters a great deal
whether those tests are telling the truth.

**There are 82 of them.** That number comes from counting every Go test
function that finds what it checks by reading the project's own committed
files — walking the source tree, the manual pages under `docs/`, or the Helm
chart — rather than a file the test itself just wrote. Anyone can repeat the
count that way and get the same set. A handful of the 82 sit on the edge of
that rule, so read it as a solid figure for that rule, not as a law of
nature.

**Sixteen of them have been deliberately attacked.** Attacked means somebody
broke, on purpose, the thing the test exists to protect, then ran the test to
see whether it went red. Four were attacked by a reviewer working
independently of the people who wrote them. Twelve more were attacked in a
round of work that set out to do nothing else.

**Several of the attacked ones did not hold**, and the failures were not
near misses. All four the reviewer attacked stayed green while a live
endpoint with no permission check, no activity record and no tier sat in the
running server: those four found endpoints by searching the source text for
one exact phrase, and the endpoint had been added through a one-line helper
instead. Two more stayed green in the later round, because they read a
hand-written list of files and the problem they hunt had been written into a
file nobody had listed. A test guarding what goes into the log had the same
shape of hole, twice over. Every one of these has been repaired.

**Nothing dangerous was hiding behind them.** Once each was repaired and
re-run against the real code, no credential leak and no way past a permission
check came out from underneath. The tests were wrong about their own reach;
they were not covering up a defect.

Sixty-six of the 82 were in neither of those two rounds. Later rounds
attacked and repaired a few more, and widened three so they see more than
they did — so the number nobody has attacked is a little under sixty-six, and
it is still the large majority. Those tests pass. Passing is the only thing
we know about them, and a test that has never been made to fail has never
been shown to work.

We are saying this because it is the sort of thing a project would normally
keep quiet. The good news in it is real — the tests were attacked at all,
the broken ones were found by us and fixed — and it is still not the same as
"the tests cover everything".

### Why you should still not read this as "audited"

We looked hard in one round and found a great deal. That is evidence that
the code had not been looked at hard before — it is not evidence that there
is nothing left. There has been no independent security assessment of this
repository. If you find something, section 8 tells you where to send it.

---

## 2. How Sharko keeps credentials out of what it says

The design idea is short: **Sharko decides what is safe to say by the
*type* of a value, never by looking at the words in it.** Checking the words
is what fails — it stops working the day a cloud provider rephrases one of
its error messages, and nobody notices because nothing goes red.

Four pieces do the work.

**One place owns the safe sentence.** `internal/credsafe` is the only place
that decides "did this come out of a credentials store?", and the only place
that owns what Sharko says when it did. It answers by matching a marker on
the error, not by reading the error's text. When an error is marked, the
error's own `Error()` method returns the fixed safe sentence — so a piece of
code that forgets to ask still gets the safe answer. The real cause stays
reachable underneath for the code that needs to know what kind of failure it
was; only the original *words* stop being reachable by accident.

**Repository addresses lose their whole sign-in section, not just the
password.** Go's standard library has a `Redacted()` method for URLs. It
hides the password and keeps the username — which is exactly wrong here,
because when a Git token is written into an address, the token is usually
*in the username slot*. So `credsafe.SafeRepoURL` drops both halves, always,
plus the query and the fragment. When it cannot parse the address well
enough to be certain which part is the secret, it returns nothing at all and
the caller says nothing about the repository. Blank is better than a guess.

**Some helpers physically cannot be misused.** The two helpers that answer
"there is no usable Git or ArgoCD connection" take neither an error nor a
string. There is no parameter into which anyone could put an error's words.
Passing one is a compilation failure, not something a reviewer has to
notice. The same trick is used for ArgoCD's own progress text: the helper
that formats it has no message field at all, so free-form text from a
provider has nowhere to travel.

**The log has the same rule at the sink, not at the call sites.** Around
eighty-eight places in the code hand an error to the logger. Rather than fix
eighty-eight places and hope nobody adds an eighty-ninth, there is one
wrapper on the logger itself. When a value handed to the logger *is* a Go
error, its words are replaced with a description built only from the error's
Go type and from fixed markers in the standard library — never from its
message. The same wrapper blanks values whose key looks like a credential
name, values shaped like a JSON web token, and long blobs that are entirely
base64.

There is one honest gap here, and it is written into the code's own notes: a
value that has already been flattened into a plain string before it reaches
the logger has no type left to classify. Those are removed one at a time,
and a check lists every place in the tree that hands a logger an error or a
provider-supplied value, so a new one cannot be added quietly.

### Private catalog repositories are not available in this preview

A Helm chart repository that needs a sign-in is a normal thing to have, and
the normal way people write one down is to put the token inside the address:

```
https://a-token@charts.example/org/charts
```

Sharko will not save that, and this is deliberate. A catalog entry is written
into a YAML file and committed to your Git repository. Git keeps everything:
once a token is in a commit it is also in every clone, every fork, every build
cache and every backup you have. Editing the file afterwards does not take it
back. So Sharko refuses the address at the point you type it, rather than
saving it and warning you about it later.

Two things follow from that.

**Catalog repository addresses must be credential-free.** The rule Sharko
applies is:

> Catalog repository URLs in the technical preview must be ones Sharko can
> read in full: a host, an optional port, and an optional path. User
> information in the address, a query string, and a fragment are all refused,
> and so is an address Sharko cannot read. Use a credential-free base URL.

That is the whole rule, and it is a bit wider than "no tokens" on purpose.
Sharko checks the *shape* of the address — is there anywhere in it a secret
could be sitting — and never tries to judge whether the text there looks
secret. A checker that judges the text stops working the first time somebody
writes a token in a shape nobody predicted. The price is that an ordinary
address like `https://charts.example/org/charts?ref=main` is refused too, even
though there is nothing secret in it. Take the `?ref=main` off and it is
accepted.

An address Sharko cannot read at all is refused for the same reason — a port
that is not a number, say. If Sharko cannot read the address, it cannot tell
whether anything is hiding in it, and "I could not tell" is not a yes.

A scheme is optional, and an `@` in the path is not user information, so
`charts.example/org/charts` and `https://charts.example/org/charts@v1` are
both accepted.

**Sharko never quietly cleans the address up for you.** It refuses and says
which field to fix. Saving a stripped-down version would mean the entry in
your repository is not what you typed, and the first you would hear of it is a
catalog that cannot reach its charts.

**If you have already committed a token, rotate or revoke it.** This matters
more than anything else on this page. If a catalog file in your repository
already carries an address with a token in it, editing the file is not enough
— the token is still sitting in your Git history, and anyone who can read the
repository, or any old clone of it, can still read the token. Revoke or
rotate that credential at the place that issued it, then remove it from the
history with a tool such as `git filter-repo`, then put a credential-free
address in the file.

While such an entry is still in your file, Sharko keeps running and every
other addon keeps working. That one addon comes back marked unusable, with
the file and field named — never the address itself. Sharko will not deploy
it, will not reach out using it, and will not show it. Any change to the
catalog file is refused until you fix that entry, because writing the file
again would put the token in a fresh commit.

**Pointing at a private repository by reference is work for after the
preview.** Naming a Kubernetes Secret or an AWS Secrets Manager entry instead
of writing the token into the address is the shape this should take, and it is
not built yet.

### Who can do what

Sharko has three roles: **viewer**, **operator** and **admin**. Destructive
and configuration-changing actions — deleting a connection, removing a
cluster, creating or deleting users, changing someone's role, turning on
automatic merging of pull requests, changing AI settings — require admin.

Signing in gives you a session token that lasts **24 hours**. There is no
refresh and no "remember me": when the day is up, you sign in again.

Long-lived API tokens for pipelines are stored only as a bcrypt hash, never
in plain text, and they survive a restart. The plain value is shown to you
once when you create it and never again. Tokens expire after 90 days by
default. An expired token gets a message naming it; an unknown or revoked
token gets a flat "unauthorized" — deliberately, so nobody can use the error
message to find out which token names exist.

Sharko creates an admin account with a random password the first time it
starts with no users configured. It does not run without authentication.

---

## 3. What Sharko can do to your clusters and your repository — the worst case

If someone took over the Sharko pod, here is what they would have. This is
read from the chart in `charts/sharko/`, not from memory.

### Inside ArgoCD, which is a separate question

Everything below is Kubernetes permissions. ArgoCD keeps its own, separate
set, and **installing Sharko does not change them.** The chart contains no
job, no install hook and no template that edits ArgoCD's permission settings.
What Sharko may do inside ArgoCD is exactly what the ArgoCD account you gave
it a token for is allowed to do, and that is yours to decide.

An earlier version of the chart did edit those settings during install. It
was removed rather than written up: one product's installer quietly narrowing
another product's shared policy is the wrong shape however carefully it is
written.

Sharko needs to read Applications, AppProjects and clusters from ArgoCD, or
it has nothing to show you. It does **not** need permission to make ArgoCD
sync an application in order for addons to be deployed. Sharko changes files
in your repository and opens a pull request; when that merges, ArgoCD picks
the change up and applies it by itself. Git says what should be running and
ArgoCD makes it so — Sharko is not standing in that path.

One thing does ask ArgoCD to sync directly: the **Restart sync** action on an
addon, and the same recovery running by itself after one of Sharko's pull
requests merges. That is a shortcut for one stuck situation, not how addons
reach your clusters. Without the permission it is unavailable and says so in
plain words — nothing is left half-done and nothing reports success it did
not have. Granting it is optional and is described in
[Letting Sharko restart a sync](operator/security.md#letting-sharko-restart-a-sync).

### Across the whole cluster Sharko runs on

Read-only, and only two things:

- **Get, list and watch ArgoCD Applications, AppProjects and
  ApplicationSets.** Nothing may be changed cluster-wide.
- **Get and list Nodes.** This is only for the node count on the dashboard.
  Turn it off with `config.nodeAccess=false` if you do not want it.

Sharko has **no cluster-wide read of Secrets**. It used to, and that was
removed — a cluster-wide `list` on Secrets hands over their contents.

### In the ArgoCD namespace (`argocd` by default)

This is where the real power is:

- **Full read and write on every Secret in that namespace** — get, list,
  watch, create, update, patch, delete. It is not restricted to the Secrets
  Sharko made. The ones Sharko is there for are the cluster connection
  Secrets, which hold the credentials ArgoCD uses to reach every cluster in
  your fleet. Anything else anyone keeps in that namespace is readable and
  writable too.
- **Patch and delete ApplicationSets, and patch Applications.** This exists
  only for taking over a fleet that an older Sharko set up, and it is
  needed because the old setup would otherwise delete running workloads when
  its inputs changed.

### In Sharko's own namespace

- **Read every Secret in the namespace** — get and list, with no list of
  names and no exception. This is the same grant described in the next
  section, and Sharko's own namespace is always on that list: the chart adds
  it whether or not you asked for it, and you cannot switch it off from
  values. `list` on Secrets hands over their contents, not only their names.
  So if you install Sharko into a namespace that already holds other
  people's Secrets, Sharko can read all of them, and so can anyone who takes
  over the Sharko pod.
- Write on a short list of Sharko's own named Secrets, and nothing else: its
  own configuration, its saved connections, its AI settings, its API tokens,
  its first-run admin password, and its migration state.
- Permission to **create** Secrets generally. Kubernetes cannot restrict
  `create` to specific names, so this cannot be narrowed further.
- Read, list, create, update and delete ConfigMaps — all of them in that
  namespace, not a named list.
- Create Kubernetes Events.

**What you can do about this.** Give Sharko a namespace of its own and put
nothing else in it. That is the whole fix, it costs nothing, and it turns
"reads every Secret in the namespace" into "reads its own Secrets". If you
install Sharko beside other things, assume Sharko can read their Secrets.

### In whichever namespaces you configure for the Kubernetes-Secrets backend

Get and list on Secrets — read-only, but **every** Secret in each of those
namespaces, not only the ones Sharko put there or was told to fetch. The
namespaces are the ones you named in `rbac.k8sSecretsProviderNamespaces`,
plus Sharko's own namespace, which is always added on top of your list.

So keep unrelated Secrets out of any namespace you name there. A namespace
on that list is a namespace Sharko can read end to end.

### Outside Kubernetes

- **Sharko holds a Git token** that can write to your addons repository, and
  it opens pull requests with it. If you turn on automatic merging, it
  merges them too. The setup guides ask for a token with `repo` scope on
  GitHub or read/write on Azure DevOps, so that token can do anything in
  that repository.
- **Sharko reaches your fleet's clusters directly** to create, update and
  delete addon Secrets there. It only deletes Secrets that carry its own
  ownership label.
- **Sharko can read your secrets backend** — AWS Secrets Manager or
  Kubernetes Secrets — for whatever it has been given access to.

### So the worst case is

Someone who takes over the Sharko pod can read the credentials for every
cluster ArgoCD knows about, and can write to your addons repository. That is
the whole fleet. They can also read every Secret in the ArgoCD namespace,
every Secret in the namespace Sharko was installed into, and every Secret in
any namespace named in `rbac.k8sSecretsProviderNamespaces` — whether or not
those Secrets have anything to do with Sharko.

Treat the namespace Sharko runs in as being as sensitive as the ArgoCD
namespace itself, and give Sharko a namespace with nothing else in it.

The pod itself is locked down as far as it goes: it runs as a non-root user
(1001), with a read-only root filesystem, no privilege escalation, and every
Linux capability dropped.

---

## 4. Connection kinds Sharko can only partly check, and the legacy one

Sharko can look at a cluster's ArgoCD connection and tell you whether it
matches what your repository says it should be. **How much of that it can
honestly check depends on where the cluster's credentials live.**

The rule underneath is worth stating on its own, because everything else
follows from it: **Sharko never builds the "expected" side of the comparison
out of the live connection it is checking.** Doing that would compare the
connection with itself, match every time, and report a badly broken
connection as fine. Where Sharko has no independent copy of the credentials,
the honest answer is a narrower check — not a guess.

**A cluster whose credentials Sharko cannot read independently can never be
reported as fully in sync.** Saying "in sync" would be a claim about
something Sharko did not look at.

There are seven kinds.

### Fully checkable — one kind only

**Credentials stored in your secrets backend.** Sharko can go and fetch the
stored sign-in file itself, rebuild the whole expected connection from it
plus your repository, and compare everything, credentials included. This is
the only kind that can be reported as fully in sync.

### Partly checkable

**EKS token.** Your secrets backend holds the details needed to *create* a
short-lived sign-in token, not a token. So there is no credential on the
expected side to compare against — nothing to compare, rather than two
things that never match. Labels and the plain connection facts are checked;
the credential blob is not. Checking creates no token. Sharko will still
offer to rewrite the connection from your backend, because "I cannot prove
this is right, but I can make it right" is a useful thing to be able to
offer.

**Pasted kubeconfig — this is the legacy one.** The cluster was registered
by pasting a kubeconfig in. Those credentials went into the ArgoCD Secret
and were kept nowhere else, so there is no second copy for Sharko to check
against. Only the labels, which come from your repository, can be checked.
Worse, if that Secret is ever deleted, Sharko cannot rebuild it — the only
copy is gone.

Pasting credentials is **turned off by default** and an admin has to switch
it on. If you have clusters registered this way, there is a guide for moving
them off it: [Migrating off pasted
credentials](operator/migrate-inline-credentials.md).

**No recorded credentials source.** Sharko does not know where this
cluster's credentials are kept, so it checks only labels and the plain
facts. **This is the one most people will actually hit.** Sharko records the
credentials source on each cluster entry, and that field did not always
exist — so *every install upgraded from an older Sharko* has records without
it, and every one of those lands here. Sharko never guesses and never fills
the field in behind your back.

**A source Sharko does not recognise.** A typo, a hand edit, or a value
written by a newer version. Same narrow check. Sharko will not treat a value
it has never heard of as one it trusts.

### Only the addon labels are checked

**Self-managed connections.** Your repository says a person maintains this
connection. Sharko only ever puts addon labels on it and never touches its
credentials, so labels are the only thing Sharko has an opinion about. If
the Secret goes missing, Sharko will not create it — that is by design, and
it now says so.

**Adopted clusters.** The connection existed in your ArgoCD before Sharko
arrived, and Sharko came in as a guest. Same stance: addon labels only.

### Nothing is checked

**A connection another tool owns.** If the ownership label names something
other than Sharko, Sharko compares nothing and offers nothing. It tells you
who holds it and stops there.

---

## 5. The activity history lives in memory and is gone when Sharko restarts

Sharko records what happened — who registered a cluster, who added an addon,
what a background job did — into an in-memory list that holds the **last
1000 entries**. That size is fixed and cannot be configured.

**A restart loses all of it.** Every entry. Sharko does not write this
history to disk, to a database, or to a file on a volume.

Two things follow, and both matter:

- **The Activity page only ever covers the time since the pod last
  started.** So does the API that reads it. So does "last repaired" on the
  managed Secrets page. If Sharko restarted an hour ago, an hour is all the
  history there is.
- **Anything busier than 1000 recorded events since the restart has already
  lost its oldest entries**, restart or no restart.

**Do not treat this as a record of what happened.** If you have to be able
to answer "who changed this, and when" weeks later — for an incident review,
for an auditor, for a customer — Sharko cannot answer it today, and no
setting makes it answer it.

**There is no workaround inside Sharko right now.** Making this history
survive a restart is real, open work. It belongs with a wider question about
what else Sharko should remember across restarts, so it is not a small
change and we are not going to pretend it is close.

What you can do outside Sharko: keep your own record of every change from
the places that already keep one — your Git repository's commit and pull
request history, which covers every change Sharko makes to what runs on your
clusters, and your Kubernetes Events, which Sharko writes to. Neither is a
substitute for the full history, and both should be treated as partial.

---

## 6. Only one copy of Sharko can run. This is a hard limit, not a suggestion

**Run exactly one replica.** The chart ships `replicaCount: 1` and you must
leave it at 1.

Sharko keeps signed-in sessions in an in-memory map inside the process.
Nothing is shared between copies. If you run a second replica:

- A person signs in and gets a token from replica A. Their next request
  lands on replica B, which has never heard of that token, and they are
  thrown back to the sign-in page. Every request is a coin toss.
- The Activity page shows whatever the replica that answered happens to
  remember, so the same page shows different history depending on which
  replica served it.
- The background jobs that maintain cluster connections would run in both
  copies at once, against the same Kubernetes objects and the same Git
  repository.

**This means Sharko cannot be made highly available today**, and it cannot
be hosted on anything that starts more than one copy of a process for you.
We have tried; it does not work.

**What a normal restart does.** Any restart — an upgrade, a node drain, a
crash — signs everybody out and empties the activity history. People sign in
again. Nothing about your clusters or your repository is affected: your
addons keep running, because ArgoCD is what deploys them, and your
repository is where the actual state lives. Sharko coming back reads
everything it needs again.

Plan upgrades for a moment when signing everyone out is fine.

---

## 7. What Sharko is supported for, and what it is not

These are two lists, not advice to interpret.

### Supported

- **Reading the code and the repository to decide whether Sharko is
  interesting.** That is what a preview is for.
- **A test or staging environment**, with clusters you would not mind
  breaking, and with credentials that only reach those clusters.
- **Trying out the GitOps workflow** — the validate, preview, open a pull
  request path — to see whether you like the shape of it.
- **A small fleet run by one team**, in an environment where an unplanned
  sign-out is an annoyance rather than an incident. Sharko is built for one
  team with one ArgoCD and one addons repository, and that is the case it
  handles properly.
- **Reading a fleet you already run.** Sharko can be pointed at an existing
  ArgoCD and used to look, and it joins an existing setup as a guest without
  taking ownership. This is the lowest-risk way to try it on something real.

### Not supported

- **Anything where the activity history is a compliance requirement.** See
  section 5. The history does not survive a restart, and there is no setting
  that changes that.
- **More than one team who must not see each other's clusters.** Sharko's
  three roles apply to the whole installation. There is no way to give an
  operator access to some clusters and not others. Everyone who can sign in
  can see the whole fleet.
- **High availability.** See section 6. One replica, by design, today.
- **Real production credentials, without your own security review first.**
  Section 3 is what a compromised Sharko gets: the credentials to your whole
  fleet, and write access to your repository. Nobody outside this project has
  checked our work.
- **Chart repositories that need a sign-in.** Catalog repository addresses
  must be credential-free — see section 2. Naming a Kubernetes Secret or an
  AWS Secrets Manager entry instead is work for after the preview.
- **Anything hosted where you do not control how many copies run.**
- **Running it and forgetting about it.** This is a preview. Expect bugs,
  expect us to change things, and expect to have to read release notes.

---

## 8. Reporting a security problem

**Please do not open a public GitHub issue for a security problem.** A public
report gives an attacker a head start on everyone who has Sharko installed.

Two private routes, both of which reach the lead maintainer:

- **GitHub's private reporting** — the Security tab on the repository,
  "Report a vulnerability". This opens a private advisory.
- **Email** — the address and the exact subject line to use are in
  [`SECURITY.md`](https://github.com/MoranWeissman/sharko/blob/main/SECURITY.md)
  at the root of the repository.

`SECURITY.md` is the full policy and it is the thing to read before you
report. It has the details this page does not repeat: what to put in a
report, what is in and out of range, and what happens after you send it. In
short: we aim to acknowledge within five working days and to have triaged
and rated the problem within ten working days after that. Those are the
targets of a pre-release project with one lead maintainer, and the policy
says so plainly rather than promising more.

Unless you ask us not to, you get the credit in the release notes.

---

## Where to go next

- [`SECURITY.md`](https://github.com/MoranWeissman/sharko/blob/main/SECURITY.md)
  — the full security policy, including which versions are supported
- [Security reference](operator/security.md) — headers, rate limiting,
  trusted proxies, and hardening for operators
- [How the activity history is kept](operator/audit-log.md)
- [Managing cluster connections yourself](operator/self-managed-connections.md)
- [Migrating off pasted credentials](operator/migrate-inline-credentials.md)
- [If you remove Sharko](operator/removing-sharko.md) — what keeps running
  and what stops
