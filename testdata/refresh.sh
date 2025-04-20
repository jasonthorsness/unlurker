#!/bin/bash
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
curl -s https://hacker-news.firebaseio.com/v0/newstories.json -o "$SCRIPT_DIR/newstories.json"
lowest_id=$(jq '.[-1]' "$SCRIPT_DIR/newstories.json")
"$SCRIPT_DIR/../bin/hn" scan --asc -c "$lowest_id" | gzip > "$SCRIPT_DIR/items.json.gz"
