#!/usr/bin/env bash
# stdout becomes context surfaced to Claude on session start.
# Per Claude Code hooks docs: SessionStart stdout is added to context.

PROJECT_DIR="${CLAUDE_PROJECT_DIR:-$(pwd)}"
cd "$PROJECT_DIR" 2>/dev/null || exit 0

CHECKPOINT_DIR=".claude/checkpoints"
LATEST="$CHECKPOINT_DIR/LATEST.md"

if [ -f "$LATEST" ]; then
  echo "## Latest checkpoint (loaded by SessionStart hook)"
  echo
  cat "$LATEST"
fi
