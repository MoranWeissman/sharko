#!/bin/bash
# UserPromptSubmit hook — scans the user's incoming message for BMAD trigger
# words. If any match, emits a <system-reminder> to stdout. Claude Code
# prepends hook stdout to the assistant's turn context, so the reminder
# lands in every turn where the trigger fires.
#
# Rationale: the LLM is prone to rationalizing around the "always use BMAD"
# rule even when it's in CLAUDE.md + memory. An in-context reminder injected
# on every matching prompt is harder to miss than a static rule.

set -euo pipefail

# Read the user's prompt from stdin (Claude Code pipes the prompt to the hook).
prompt=$(cat)

# Fast-path: if prompt is very short (<5 chars), skip — it's probably "yes" /
# "ok" / a greenlight for prior work, not a new feature kickoff.
if [ "${#prompt}" -lt 5 ]; then
  exit 0
fi

# Case-insensitive trigger patterns. These match the user intent categories
# from CLAUDE.md's BMAD skill map.
# Word boundaries avoid false positives (e.g., "plan" inside "explain").
trigger_patterns=(
  '\bplan\b'
  '\bplanning\b'
  '\bbrainstorm\b'
  '\bdesign\b'
  '\bimplement\b'
  '\bbuild\b(\s+this|\s+a|\s+the|\s+it|\s+out)?'
  '\bstart\b(\s+v?[0-9]|\s+the|\s+new|\s+a|\s+working)'
  '\bdo it\b'
  '\bship\b(\s+it|\s+this)?'
  '\bcreate\s+prd\b'
  '\barchitect(ure)?\b'
  '\breview\s+(the\s+)?code\b'
  '\bfeature\b'
  '\bepic\b'
  '\bsprint\b'
  '\bstor(y|ies)\b'
  '\bwhat.?s next\b'
  '\bnext step\b'
  '\bcarryover'
  '\bbundle\b'
)

matched_trigger=""
for pattern in "${trigger_patterns[@]}"; do
  if echo "$prompt" | grep -qiE "$pattern"; then
    matched_trigger=$(echo "$prompt" | grep -oiE "$pattern" | head -1 | tr '[:upper:]' '[:lower:]')
    break
  fi
done

if [ -z "$matched_trigger" ]; then
  exit 0
fi

# Emit the system reminder. Claude Code will prepend this to the assistant's
# context for this turn.
cat <<EOF
<system-reminder>
BMAD ENFORCEMENT — trigger word detected in user prompt: "${matched_trigger}"

This user request matches a BMAD kickoff pattern. BEFORE doing any code
dispatch, planning, or feature kickoff work, you MUST invoke the matching
BMAD skill first. Do not rationalize around this rule.

Quick reference:
- "plan" / "start" / "build" / "do it" / "ship" / "feature" / "bundle" / "carryover"
  → invoke bmad-sprint-planning (or bmad-create-epics-and-stories if breaking a
  design doc into epics)
- "brainstorm" / "design" / "architecture"
  → invoke bmad-brainstorming (or bmad-party-mode for complex trade-offs)
- "review the code" / after completing a feature
  → invoke bmad-code-review
- Unsure which skill → invoke bmad-help

ALSO: every agent dispatch MUST embed the relevant .claude/team/*.md role
file(s) in the prompt. No exceptions.

If you ignore this reminder and proceed without BMAD, you are violating the
user's explicit enforcement instruction. This reminder is added to every
triggering prompt precisely because the rule has been missed before.

See CLAUDE.md §"MANDATORY BMAD FLOW" for the full rule.
</system-reminder>
EOF
