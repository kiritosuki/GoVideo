#!/usr/bin/env bash
set -euo pipefail

# 在本机运行集成测试，在远程Linux虚拟机中启动测试中间件。
# 默认依赖 ~/.ssh/config 中存在 Host master。

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

SSH_HOST="${SSH_HOST:-master}"
REMOTE_DIR="${REMOTE_DIR:-~/mysrc/project/GoVideo}"
COMPOSE_FILE="deploy/docker-compose.integration.yml"
CONFIG_PATH="${CONFIG_PATH:-configs/config-integration.yaml}"
RUN_DIR="${RUN_DIR:-.run}"
JSON_REPORT="${JSON_REPORT:-$RUN_DIR/integration-test.json}"
COVER_PROFILE="${COVER_PROFILE:-$RUN_DIR/integration-cover.out}"
COVER_FUNC="${COVER_FUNC:-$RUN_DIR/integration-cover.txt}"
COVER_PKG="${COVER_PKG:-./internal/...}"
HEALTH_TIMEOUT="${HEALTH_TIMEOUT:-120}"

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

JSON_REPORT="$LOCAL_RUN_DIR/$(basename "$JSON_REPORT")"
COVER_PROFILE="$LOCAL_RUN_DIR/$(basename "$COVER_PROFILE")"
COVER_FUNC="$LOCAL_RUN_DIR/$(basename "$COVER_FUNC")"

STARTED_REMOTE=0
TEST_EXIT_CODE=0

log() {
	printf '[integration] %s\n' "$*"
}

fail() {
	printf '[integration] ERROR: %s\n' "$*" >&2
	exit 1
}

remote() {
	ssh "$SSH_HOST" "$@"
}

cleanup() {
	if [ "$STARTED_REMOTE" -eq 1 ]; then
		log 'Stop integration middleware and remove volumes'
		remote "cd $REMOTE_DIR && docker compose -f $COMPOSE_FILE down -v" || true
	fi
}
trap cleanup EXIT

require_file() {
	if [ ! -f "$1" ]; then
		fail "missing required file: $1"
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
			log 'All integration middleware containers are healthy'
			return 0
		fi
		printf '.'
		sleep 3
	done
	printf '\n'
	remote "cd $REMOTE_DIR && docker compose -f $COMPOSE_FILE ps" || true
	fail "middleware containers did not become healthy within ${timeout_seconds}s"
}

summarize_tests() {
	local json_file="$1"
	local cover_profile="$2"
	local passed failed skipped total status coverage

	passed="$(awk -F'"' '/"Action":"pass"/ && /"Test":/ {pass++} END{print pass+0}' "$json_file" 2>/dev/null || printf '0')"
	failed="$(awk -F'"' '/"Action":"fail"/ && /"Test":/ {fail++} END{print fail+0}' "$json_file" 2>/dev/null || printf '0')"
	skipped="$(awk -F'"' '/"Action":"skip"/ && /"Test":/ {skip++} END{print skip+0}' "$json_file" 2>/dev/null || printf '0')"
	total=$((passed + failed + skipped))

	if [ "$TEST_EXIT_CODE" -eq 0 ]; then
		status='PASS'
	else
		status='FAIL'
	fi

	coverage='N/A'
	if [ -f "$cover_profile" ]; then
		if go tool cover -func="$cover_profile" > "$COVER_FUNC" 2>/dev/null; then
			coverage="$(awk '/^total:/ {print $3}' "$COVER_FUNC")"
		fi
	fi

	printf '\n'
	printf 'Integration Test Result\n'
	printf 'Status: %s\n' "$status"
	printf 'Passed: %s\n' "$passed"
	printf 'Failed: %s\n' "$failed"
	printf 'Skipped: %s\n' "$skipped"
	printf 'Total Tests: %s\n' "$total"
	printf 'Coverage: %s\n' "$coverage"
	printf 'Cover Packages: %s\n' "$COVER_PKG"
	printf 'JSON Report: %s\n' "$json_file"
	printf 'Coverage Profile: %s\n' "$cover_profile"
	printf 'Coverage Detail: %s\n' "$COVER_FUNC"
}

main() {
	require_file "$LOCAL_COMPOSE_FILE"
	require_file "$LOCAL_CONFIG_PATH"
	mkdir -p "$LOCAL_RUN_DIR"

	log "Check SSH connection: $SSH_HOST"
	remote "true" || fail "ssh connection failed: $SSH_HOST"

	log "Prepare remote directory: $REMOTE_DIR/deploy"
	remote "mkdir -p $REMOTE_DIR/deploy"

	log "Sync docker compose to remote"
	scp "$LOCAL_COMPOSE_FILE" "$SSH_HOST:$REMOTE_DIR/$COMPOSE_FILE" >/dev/null

	log 'Start integration middleware on remote'
	remote "cd $REMOTE_DIR && docker compose -f $COMPOSE_FILE down -v >/dev/null 2>&1 || true"
	remote "cd $REMOTE_DIR && docker compose -f $COMPOSE_FILE up -d"
	STARTED_REMOTE=1

	wait_remote_services "$HEALTH_TIMEOUT"

	log 'Run integration tests locally'
	set +e
	cd "$PROJECT_ROOT"
	CONFIG_PATH="$LOCAL_CONFIG_PATH" go test \
		-tags=integration \
		-coverpkg="$COVER_PKG" \
		-coverprofile="$COVER_PROFILE" \
		-json \
		./tests/integration/... > "$JSON_REPORT"
	TEST_EXIT_CODE=$?
	set -e

	summarize_tests "$JSON_REPORT" "$COVER_PROFILE"
	exit "$TEST_EXIT_CODE"
}

main "$@"
