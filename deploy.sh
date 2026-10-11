#!/bin/bash
# Generates config from quicknotes.config.json, then deploys the web app and Firestore rules.
#   ./deploy.sh                 (needs `firebase login` once)
set -euo pipefail
cd "$(dirname "$0")"
python3 scripts/configure.py
firebase deploy --only hosting,firestore:rules "$@"
