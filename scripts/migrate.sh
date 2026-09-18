#!/bin/bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

cd "${PROJECT_ROOT}"

usage() {
  cat <<EOF
WhisperServer Database Compatibility & Migration Tool

Usage:
  ./scripts/migrate.sh [options]

Options:
  --check               Run read-only diagnostic check without modifying the database.
  --migrate             Execute migration (compress JSON, migrate blobs, sync FTS5, vacuum).
  --db <path>           Specify custom SQLite database path (default: auto-detected from app.conf).
  --conf <dir>          Specify project root directory containing app.conf (default: current dir).
  --skip-vacuum         Skip SQLite VACUUM step after migration.
  --skip-backup         Skip creating backup file before migration.
  -h, --help            Show this help message.

Examples:
  ./scripts/migrate.sh --check
  ./scripts/migrate.sh --migrate
  ./scripts/migrate.sh --db .run/whisper.db --migrate
EOF
}

CHECK_FLAG=""
MIGRATE_FLAG=""
DB_ARG=""
CONF_ARG=""
EXTRA_ARGS=()

while [[ $# -gt 0 ]]; do
  case "$1" in
    --check)
      CHECK_FLAG="-check"
      shift
      ;;
    --migrate)
      MIGRATE_FLAG="-migrate"
      shift
      ;;
    --db)
      DB_ARG="-db $2"
      shift 2
      ;;
    --conf)
      CONF_ARG="-conf $2"
      shift 2
      ;;
    --skip-vacuum)
      EXTRA_ARGS+=("-skip-vacuum")
      shift
      ;;
    --skip-backup)
      EXTRA_ARGS+=("-skip-backup")
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "Unknown option: $1"
      usage
      exit 1
      ;;
  esac
done

# Default to --check if neither --check nor --migrate is provided
if [[ -z "${CHECK_FLAG}" && -z "${MIGRATE_FLAG}" ]]; then
  echo "[INFO] Neither --check nor --migrate specified. Defaulting to diagnostic check (--check)."
  CHECK_FLAG="-check"
fi

CMD_ARGS=()
if [[ -n "${CHECK_FLAG}" ]]; then
  CMD_ARGS+=("${CHECK_FLAG}")
fi
if [[ -n "${MIGRATE_FLAG}" ]]; then
  CMD_ARGS+=("${MIGRATE_FLAG}")
fi
if [[ -n "${DB_ARG}" ]]; then
  CMD_ARGS+=(${DB_ARG})
fi
if [[ -n "${CONF_ARG}" ]]; then
  CMD_ARGS+=(${CONF_ARG})
fi
if [[ ${#EXTRA_ARGS[@]} -gt 0 ]]; then
  CMD_ARGS+=("${EXTRA_ARGS[@]}")
fi

go run ./src/cmd/migrate "${CMD_ARGS[@]}"
