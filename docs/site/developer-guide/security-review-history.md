# Security review history

**This page is a historical record, not a description of how Sharko behaves
today.** It keeps the write-up of the security-review round that ran before
the v4 technical preview, so that the reasoning behind it is not lost. Every
statement below is pinned to **20 August 2026** and to commit **`6c0e8410`**.
Nothing on this page should be read as a current product claim.

**Recorded:** 30 August 2026, from the write-up that shipped on the preview
page up to that date. For where Sharko stands now, read
[Current limitations and supported use](../technical-preview.md).

## The claim as it stood on 20 August 2026

As of 20 August 2026, at commit `6c0e8410`, the project's claim was **"no
known", not "none"**: no *known* credential leaks, no *known* ways to get past
Sharko's permission checks, and no *known* places where Sharko reported
success for work that had failed.

That was a much smaller claim than it sounds, and the rest of this page is
why.

## What that round found and fixed, up to commit `6c0e8410`

The round of work that produced the original write-up went looking for these
problems on purpose, and found a long series of them. Not one of them was
found by a test going red. Every one was found by a person reading the code
and asking "what exactly travels out of here?".

**A Git repository address usually has the password inside it.** People write
repository addresses like
`https://x-access-token:SECRET@github.example/org/repo.git`. The token is part
of the address. Anything that prints the address prints the token, and at the
place where the code did it, it looked completely innocent — it looked like
helpfully saying *which* repository had a problem.

At that point, the address was being handed out by:

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
batch of clusters all got halfway and stopped, it wrote "nothing changed" into
the activity history. When an adoption of several clusters all got halfway and
stopped, it wrote "success, changes applied". Five commands printed "done" and
exited with a success code for work that had failed.

**Three numbers on the metrics page published a zero** when nothing was
measuring them at all, so a dashboard showed a confident, wrong number instead
of a gap.

All of those were fixed at commit `6c0e8410`, and each fix was pinned by a
test that fails if it comes back.

## How much the wide-reaching tests had themselves been tested, as of 20 August 2026

Some of Sharko's tests do not check one example. They claim to have looked at
everything: every endpoint, every message, every file in a tree, every entry
in a list. A test like that is the reason a whole class of problem can be
called gone rather than "gone in the case we tried". So it mattered a great
deal whether those tests were telling the truth.

**As of 20 August 2026 there were 82 of them.** That number came from counting
every Go test function that finds what it checks by reading the project's own
committed files — walking the source tree, the manual pages under `docs/`, or
the Helm chart — rather than a file the test itself just wrote. Anyone can
repeat the count that way and get the same set. A handful of the 82 sat on the
edge of that rule, so read it as a solid figure for that rule, not as a law of
nature.

**Sixteen of them had been deliberately attacked.** Attacked means somebody
broke, on purpose, the thing the test exists to protect, then ran the test to
see whether it went red. Four were attacked by a reviewer working
independently of the people who wrote them. Twelve more were attacked in a
round of work that set out to do nothing else.

**Several of the attacked ones did not hold**, and the failures were not near
misses. All four the reviewer attacked stayed green while a live endpoint with
no permission check, no activity record and no tier sat in the running server:
those four found endpoints by searching the source text for one exact phrase,
and the endpoint had been added through a one-line helper instead. Two more
stayed green in the later round, because they read a hand-written list of files
and the problem they hunt had been written into a file nobody had listed. A
test guarding what goes into the log had the same shape of hole, twice over.
Every one of these was repaired.

**Nothing dangerous was hiding behind them.** Once each was repaired and
re-run against the real code, no credential leak and no way past a permission
check came out from underneath. The tests were wrong about their own reach;
they were not covering up a defect.

Sixty-six of the 82 were in neither of those two rounds. Later rounds attacked
and repaired a few more, and widened three so they see more than they did — so
the number nobody had attacked was a little under sixty-six, and it was still
the large majority. Those tests passed. Passing was the only thing known about
them, and a test that has never been made to fail has never been shown to
work.

This was written down because it is the sort of thing a project would normally
keep quiet. The good news in it was real — the tests were attacked at all, the
broken ones were found by the project and fixed — and it was still not the
same as "the tests cover everything".

## Why that round was not an audit

The round looked hard once and found a great deal. That is evidence that the
code had not been looked at hard before — it is not evidence that there was
nothing left. As of 20 August 2026 there had been no independent security
assessment of this repository, and none has been completed since.

If you find something, do not open a public GitHub issue for it.
[Reporting a security problem](../technical-preview.md#6-reporting-a-security-problem)
says where to send it, and
[`SECURITY.md`](https://github.com/MoranWeissman/sharko/blob/main/SECURITY.md)
is the full policy.
