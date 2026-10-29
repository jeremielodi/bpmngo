#!/usr/bin/env bash
# A first workflow on a running BpmnGo, through the Camunda 8 style REST API
# (/v2) only: deploy examples/hello-world.bpmn, start an instance, complete
# its user task, see it finish.
#
#   ./scripts/demo.sh        # does every step
#   ./scripts/demo.sh --ui   # stops once the task is waiting: complete it in
#                            # the web app (Tasks page), then watch the instance
#
# BPMNGO_URL (default http://127.0.0.1:8080), and the admin account from
# SUPER_USER_EMAIL / SUPER_USER_PASSWORD (defaults admin@goflow.com / admin123).
set -euo pipefail

URL="${BPMNGO_URL:-http://127.0.0.1:${SERVER_PORT:-8080}}"
USER_EMAIL="${SUPER_USER_EMAIL:-admin@goflow.com}"
USER_PASSWORD="${SUPER_USER_PASSWORD:-admin123}"
cd "$(dirname "$0")/.." # a relative path for curl -F, whatever the shell's path style
BPMN=examples/hello-world.bpmn
UI_ONLY=false
[ "${1:-}" = "--ui" ] && UI_ONLY=true

api() { # api METHOD PATH [JSON]
  curl -sS --fail-with-body -u "$USER_EMAIL:$USER_PASSWORD" -X "$1" "$URL$2" \
    -H 'Content-Type: application/json' ${3:+-d "$3"}
}
field() { # field NAME < json — the value of "NAME" (answers here hold one)
  sed -n "s/.*\"$1\":\"\{0,1\}\([^\",}]*\)\"\{0,1\}.*/\1/p" | head -n 1
}

printf 'Waiting for BpmnGo at %s ' "$URL"
for _ in $(seq 1 60); do
  curl -sf "$URL/health" >/dev/null && break
  printf '.'; sleep 2
done
curl -sf "$URL/health" >/dev/null || { echo; echo "BpmnGo is not answering at $URL"; exit 1; }
echo ' ok'

echo
echo '1. Deploy examples/hello-world.bpmn'
curl -sS --fail-with-body -u "$USER_EMAIL:$USER_PASSWORD" -F "resources=@$BPMN" "$URL/v2/resources/deployments" >/dev/null
echo '   POST /v2/resources/deployments -> deployed process "hello_world"'

echo
echo '2. Start an instance'
INSTANCE=$(api POST /v2/process-instances '{"processDefinitionId":"hello_world","variables":{"requester":"Ada","amount":120}}' | field processInstanceKey)
echo "   POST /v2/process-instances -> processInstanceKey $INSTANCE"

echo
echo '3. Its user task "Review the request" is waiting'
TASK=''
for _ in $(seq 1 20); do
  TASK=$(api POST /v2/user-tasks/search "{\"filter\":{\"processInstanceKey\":\"$INSTANCE\",\"state\":\"CREATED\"}}" | field userTaskKey)
  [ -n "$TASK" ] && break
  sleep 0.5
done
[ -n "$TASK" ] || { echo "   no user task found for instance $INSTANCE"; exit 1; }
echo "   POST /v2/user-tasks/search -> userTaskKey $TASK"

if $UI_ONLY; then
  echo
  echo "Now open the web app (http://127.0.0.1:${FRONTEND_PORT:-3000}, $USER_EMAIL), complete the task"
  echo "on the Tasks page, and follow the instance on the Process Instances page."
  exit 0
fi

echo
echo '4. Approve it'
api POST "/v2/user-tasks/$TASK/completion" '{"variables":{"approved":true}}' >/dev/null
echo "   POST /v2/user-tasks/$TASK/completion {approved: true}"

echo
echo '5. The instance has finished'
STATE=''
for _ in $(seq 1 20); do
  STATE=$(api GET "/v2/process-instances/$INSTANCE" | field state)
  [ "$STATE" = COMPLETED ] && break
  sleep 0.5
done
echo "   GET /v2/process-instances/$INSTANCE -> state $STATE"
[ "$STATE" = COMPLETED ] || exit 1

echo
echo "Done. See it in the web app: http://127.0.0.1:${FRONTEND_PORT:-3000} (History page)."
