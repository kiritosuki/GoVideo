#!/usr/bin/env bash
set -euo pipefail

# 在本机运行k6性能测试，在远程Linux虚拟机中启动测试中间件。
# 默认依赖 ~/.ssh/config 中存在 Host master。

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

SSH_HOST="${SSH_HOST:-master}"
REMOTE_DIR="${REMOTE_DIR:-~/mysrc/project/GoVideo}"
COMPOSE_FILE="${COMPOSE_FILE:-deploy/docker-compose.integration.yml}"
CONFIG_PATH="${CONFIG_PATH:-configs/config-integration.yaml}"
RUN_DIR="${RUN_DIR:-.run/performance}"
BASE_URL="${BASE_URL:-http://127.0.0.1:8080}"
HEALTH_TIMEOUT="${HEALTH_TIMEOUT:-120}"
START_APP="${START_APP:-1}"
STOP_REMOTE="${STOP_REMOTE:-1}"
DOWN_VOLUMES="${DOWN_VOLUMES:-1}"
COOLDOWN_SECONDS="${COOLDOWN_SECONDS:-15}"
STOP_ON_FAILURE="${STOP_ON_FAILURE:-1}"
APP_LOG="${APP_LOG:-0}"

TARGET="${1:-all}"

if [[ "$COMPOSE_FILE" = /* ]]; then
	LOCAL_COMPOSE_FILE="$COMPOSE_FILE"
else
	LOCAL_COMPOSE_FILE="$PROJECT_ROOT/$COMPOSE_FILE"
fi

if [[ "$CONFIG_PATH" = /* ]]; then
	LOCAL_CONFIG_PATH="$CONFIG_PATH"
else
	LOCAL_CONFIG_PATH="$PROJECT_ROOT/$CONFIG_PATH"
fi

if [[ "$RUN_DIR" = /* ]]; then
	LOCAL_RUN_DIR="$RUN_DIR"
else
	LOCAL_RUN_DIR="$PROJECT_ROOT/$RUN_DIR"
fi

API_PID=""
WORKER_PID=""
STARTED_REMOTE=0
TEST_EXIT_CODE=0

log() {
	printf '[performance] %s\n' "$*"
}

fail() {
	printf '[performance] ERROR: %s\n' "$*" >&2
	exit 1
}

remote() {
	ssh "$SSH_HOST" "$@"
}

compose_down_cmd() {
	if [ "$DOWN_VOLUMES" = "1" ]; then
		printf 'docker compose -f %s down -v' "$COMPOSE_FILE"
	else
		printf 'docker compose -f %s down' "$COMPOSE_FILE"
	fi
}

cleanup() {
	if [ -n "$WORKER_PID" ]; then
		log "Stop worker process: $WORKER_PID"
		kill "$WORKER_PID" >/dev/null 2>&1 || true
		wait "$WORKER_PID" >/dev/null 2>&1 || true
	fi
	if [ -n "$API_PID" ]; then
		log "Stop API process: $API_PID"
		kill "$API_PID" >/dev/null 2>&1 || true
		wait "$API_PID" >/dev/null 2>&1 || true
	fi
	if [ "$STARTED_REMOTE" -eq 1 ] && [ "$STOP_REMOTE" = "1" ]; then
		log 'Stop performance middleware'
		remote "cd $REMOTE_DIR && $(compose_down_cmd)" || true
	fi
}
trap cleanup EXIT

require_file() {
	if [ ! -f "$1" ]; then
		fail "missing required file: $1"
	fi
}

require_cmd() {
	if ! command -v "$1" >/dev/null 2>&1; then
		fail "missing command: $1"
	fi
}

wait_remote_services() {
	local timeout_seconds="${1:-120}"
	local deadline=$((SECONDS + timeout_seconds))
	local statuses

	log 'Wait MySQL/Redis/RabbitMQ containers healthy'
	while [ "$SECONDS" -lt "$deadline" ]; do
		statuses="$(remote "docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' govideo-it-mysql govideo-it-redis govideo-it-rabbitmq 2>/dev/null" || true)"
		if printf '%s\n' "$statuses" | awk 'BEGIN{ok=1; n=0} {n++; if ($0!="healthy") ok=0} END{exit !(ok && n==3)}'; then
			log 'All middleware containers are healthy'
			return 0
		fi
		printf '.'
		sleep 3
	done
	printf '\n'
	remote "cd $REMOTE_DIR && docker compose -f $COMPOSE_FILE ps" || true
	fail "middleware containers did not become healthy within ${timeout_seconds}s"
}

wait_api_ready() {
	local timeout_seconds="${1:-120}"
	local deadline=$((SECONDS + timeout_seconds))

	log "Wait API ready: $BASE_URL"
	while [ "$SECONDS" -lt "$deadline" ]; do
		if curl -fsS \
			-H 'Content-Type: application/json' \
			-d '{"limit":1,"latest_time":0}' \
			"$BASE_URL/feed/listLatest" >/dev/null 2>&1; then
			log 'API is ready'
			return 0
		fi
		printf '.'
		sleep 2
	done
	printf '\n'
	fail "API did not become ready within ${timeout_seconds}s"
}

start_app() {
	log 'Start API and worker locally'
	cd "$PROJECT_ROOT"
	if [ "$APP_LOG" = "1" ]; then
		CONFIG_PATH="$LOCAL_CONFIG_PATH" go run ./cmd > "$LOCAL_RUN_DIR/api.log" 2>&1 &
	else
		CONFIG_PATH="$LOCAL_CONFIG_PATH" go run ./cmd >/dev/null 2>&1 &
	fi
	API_PID=$!

	wait_api_ready "$HEALTH_TIMEOUT"

	if [ "$APP_LOG" = "1" ]; then
		CONFIG_PATH="$LOCAL_CONFIG_PATH" go run ./cmd/worker > "$LOCAL_RUN_DIR/worker.log" 2>&1 &
	else
		CONFIG_PATH="$LOCAL_CONFIG_PATH" go run ./cmd/worker >/dev/null 2>&1 &
	fi
	WORKER_PID=$!
	sleep 2

	if ! kill -0 "$WORKER_PID" >/dev/null 2>&1; then
		if [ "$APP_LOG" = "1" ]; then
			fail "worker process exited early, see $LOCAL_RUN_DIR/worker.log"
		fi
		fail "worker process exited early; rerun with APP_LOG=1 to save worker log"
	fi
}

script_path() {
	case "$1" in
		list_latest)
			printf '%s/tests/performance/list_latest.js' "$PROJECT_ROOT"
			;;
		list_by_popularity)
			printf '%s/tests/performance/list_by_popularity.js' "$PROJECT_ROOT"
			;;
		video_get_detail)
			printf '%s/tests/performance/video_get_detail.js' "$PROJECT_ROOT"
			;;
		comment_publish)
			printf '%s/tests/performance/comment_publish.js' "$PROJECT_ROOT"
			;;
		*)
			fail "unknown target: $1"
			;;
	esac
}

run_k6() {
	local name="$1"
	local script
	local summary
	local output

	script="$(script_path "$name")"
	summary="$LOCAL_RUN_DIR/${name}-summary.json"
	output="$LOCAL_RUN_DIR/${name}-output.txt"

	log "Run k6 script: $name"
	set +e
	BASE_URL="$BASE_URL" k6 run \
		--summary-export "$summary" \
		"$script" | tee "$output"
	local code=${PIPESTATUS[0]}
	set -e

	if [ "$code" -ne 0 ]; then
		TEST_EXIT_CODE="$code"
	fi
	return "$code"
}

run_k6_with_guard() {
	local name="$1"
	if ! run_k6 "$name"; then
		if [ "$STOP_ON_FAILURE" = "1" ]; then
			log "Stop remaining scripts because $name failed"
			return 1
		fi
	fi
	if [ "$COOLDOWN_SECONDS" -gt 0 ]; then
		log "Cooldown ${COOLDOWN_SECONDS}s before next script"
		sleep "$COOLDOWN_SECONDS"
	fi
	return 0
}

main() {
	require_file "$LOCAL_COMPOSE_FILE"
	require_file "$LOCAL_CONFIG_PATH"
	require_cmd ssh
	require_cmd scp
	require_cmd curl
	require_cmd k6

	mkdir -p "$LOCAL_RUN_DIR"
	if [ "$APP_LOG" != "1" ]; then
		: > "$LOCAL_RUN_DIR/api.log"
		: > "$LOCAL_RUN_DIR/worker.log"
	fi

	log "Check SSH connection: $SSH_HOST"
	remote "true" || fail "ssh connection failed: $SSH_HOST"

	log "Prepare remote directory: $REMOTE_DIR/deploy"
	remote "mkdir -p $REMOTE_DIR/deploy"

	log 'Sync docker compose to remote'
	scp "$LOCAL_COMPOSE_FILE" "$SSH_HOST:$REMOTE_DIR/$COMPOSE_FILE" >/dev/null

	log 'Start middleware on remote'
	remote "cd $REMOTE_DIR && $(compose_down_cmd) >/dev/null 2>&1 || true"
	remote "cd $REMOTE_DIR && docker compose -f $COMPOSE_FILE up -d"
	STARTED_REMOTE=1

	wait_remote_services "$HEALTH_TIMEOUT"

	if [ "$START_APP" = "1" ]; then
		start_app
	else
		wait_api_ready "$HEALTH_TIMEOUT"
	fi

	case "$TARGET" in
		all)
			run_k6_with_guard list_latest || true
			if [ "$TEST_EXIT_CODE" -eq 0 ] || [ "$STOP_ON_FAILURE" != "1" ]; then
				run_k6_with_guard list_by_popularity || true
			fi
			if [ "$TEST_EXIT_CODE" -eq 0 ] || [ "$STOP_ON_FAILURE" != "1" ]; then
				run_k6_with_guard video_get_detail || true
			fi
			if [ "$TEST_EXIT_CODE" -eq 0 ] || [ "$STOP_ON_FAILURE" != "1" ]; then
				run_k6_with_guard comment_publish || true
			fi
			;;
		list_latest|list_by_popularity|video_get_detail|comment_publish)
			run_k6 "$TARGET" || true
			;;
		*)
			fail "unknown target: $TARGET"
			;;
	esac

	printf '\n'
	printf 'Performance Test Result\n'
	printf 'Target: %s\n' "$TARGET"
	printf 'Status: %s\n' "$([ "$TEST_EXIT_CODE" -eq 0 ] && printf PASS || printf FAIL)"
	printf 'Output Dir: %s\n' "$LOCAL_RUN_DIR"
	printf 'Base URL: %s\n' "$BASE_URL"
	exit "$TEST_EXIT_CODE"
}

main "$@"
