#!/bin/bash
# Enforce: tech lead (main session) NEVER writes code directly.
# Subagents are allowed. Main session can still edit docs/config/settings.

INPUT=$(cat)

# Extract fields
AGENT_ID=$(echo "$INPUT" | jq -r '.agent_id // empty')
FILE_PATH=$(echo "$INPUT" | jq -r '.tool_input.file_path // empty')

# Allow if it's a subagent
if [ -n "$AGENT_ID" ]; then
  exit 0
fi

# No file path = allow
if [ -z "$FILE_PATH" ]; then
  exit 0
fi

# Block code files in main session
if [[ "$FILE_PATH" =~ \.(go|ts|tsx|js|jsx|css)$ ]]; then
  cat <<EOF
{
  "hookSpecificOutput": {
    "hookEventName": "PreToolUse",
    "permissionDecision": "deny",
    "permissionDecisionReason": "BLOCKED: Tech lead cannot write code directly. Dispatch a subagent with the appropriate .claude/team/ role file. Even 1-line changes go through an agent. See .claude/team/tech-lead.md 'DO — Agent Dispatch Rules'."
  }
}
EOF
  exit 0
fi

# Allow everything else (docs, config, yaml, md, json, settings, team files)
exit 0
