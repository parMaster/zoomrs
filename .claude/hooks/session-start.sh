#!/bin/bash
# Install the parmaster-claude-dlc marketplace and the plugins enabled in
# .claude/settings.json, which cloud sessions don't install on their own.
set -euo pipefail

if [ "${CLAUDE_CODE_REMOTE:-}" != "true" ]; then
  exit 0
fi

MARKETPLACE=parmaster-claude-dlc
PLUGINS=(planning brainstorm style git-tools global-rules)

if ! claude plugin marketplace list 2>/dev/null | grep -q "$MARKETPLACE"; then
  claude plugin marketplace add parmaster/claude-dlc >&2
fi

for p in "${PLUGINS[@]}"; do
  claude plugin install "$p@$MARKETPLACE" >&2 || echo "failed to install $p@$MARKETPLACE" >&2
done
