#!/usr/bin/env bash
# End-to-end proof of the Phase 0 milestone: bring up the real controlplane
# and worker images via docker-compose, let the worker self-register with
# no manual step, register an Environment against a real (emulated) AWS
# account, grant it to the worker, and confirm the worker actually calls
# sts:AssumeRole and succeeds — the same command locally as in CI.
set -euo pipefail

COMPOSE="docker compose -f docker-compose.yml -f scripts/docker-compose.e2e.yml"
CP_URL="http://localhost:8080"
FLOCI_URL="http://localhost:4566"
ADMIN_PASSWORD="correct-horse-battery-staple" # pragma: allowlist secret
EXTERNAL_ID="e2e-external-id"
ACCOUNT_ID="000000000000"

cleanup() {
  status=$?
  if [ "$status" -ne 0 ]; then
    echo "--- e2e failed, dumping compose logs ---"
    $COMPOSE logs
  fi
  $COMPOSE down -v
  exit "$status"
}
trap cleanup EXIT

wait_for() {
  local desc="$1" url="$2" attempts="${3:-30}"
  for _ in $(seq 1 "$attempts"); do
    if curl -sf "$url" >/dev/null 2>&1; then
      return 0
    fi
    sleep 2
  done
  echo "timed out waiting for $desc ($url)"
  return 1
}

echo "--- building and starting the stack ---"
$COMPOSE up -d --build

echo "--- waiting for controlplane ---"
wait_for "controlplane" "$CP_URL/v1/setup"

echo "--- waiting for Floci ---"
wait_for "Floci" "$FLOCI_URL/_localstack/health"

echo "--- running first-run setup ---"
SETUP=$(curl -sf -X POST "$CP_URL/v1/setup" -H "Content-Type: application/json" \
  -d "{\"tenant_name\":\"e2e\",\"admin_username\":\"admin\",\"admin_password\":\"$ADMIN_PASSWORD\"}")
TOKEN=$(echo "$SETUP" | jq -r .token)

echo "--- creating tier IAM roles in Floci ---"
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_DEFAULT_REGION=us-east-1 AWS_ENDPOINT_URL="$FLOCI_URL"
TRUST_POLICY=$(cat <<EOF
{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"AWS":"arn:aws:iam::$ACCOUNT_ID:root"},"Action":"sts:AssumeRole","Condition":{"StringEquals":{"sts:ExternalId":"$EXTERNAL_ID"}}}]}
EOF
)
role_arn() {
  aws iam create-role --role-name "$1" --assume-role-policy-document "$TRUST_POLICY" --query Role.Arn --output text
}
ARN_READ_ONLY=$(role_arn "e2e-read-only")
ARN_HITL=$(role_arn "e2e-human-in-the-loop")
ARN_AUTONOMOUS=$(role_arn "e2e-autonomous")

echo "--- registering the Environment ---"
curl -sf -X POST "$CP_URL/v1/environments" -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" -d "$(jq -n \
  --arg account_id "$ACCOUNT_ID" --arg ext_id "$EXTERNAL_ID" \
  --arg ro "$ARN_READ_ONLY" --arg hitl "$ARN_HITL" --arg auto "$ARN_AUTONOMOUS" '{
  name: "e2e",
  provider: "aws",
  region: "us-east-1",
  account_ref: $account_id,
  external_id: $ext_id,
  trust_anchor: ("arn:aws:iam::" + $account_id + ":role/e2e-worker"),
  role_arns: {read_only: $ro, human_in_the_loop: $hitl, autonomous: $auto}
}')" >/dev/null

echo "--- waiting for the worker to self-register ---"
for _ in $(seq 1 30); do
  WORKER_ID=$(curl -sf "$CP_URL/v1/workers" -H "Authorization: Bearer $TOKEN" | jq -r '.[0].worker_credential_id // empty')
  [ -n "$WORKER_ID" ] && break
  sleep 2
done
if [ -z "${WORKER_ID:-}" ]; then
  echo "worker never self-registered"
  exit 1
fi

echo "--- granting the worker access to the environment ---"
curl -sf -X POST "$CP_URL/v1/workers/$WORKER_ID/environments" -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"environments": ["e2e"]}' >/dev/null

echo "--- triggering the connectivity check ---"
VERIFICATION=$(curl -sf -X POST "$CP_URL/v1/environments/e2e/verify" -H "Authorization: Bearer $TOKEN")
VERIFICATION_ID=$(echo "$VERIFICATION" | jq -r .verification_id)

echo "--- waiting for the worker to claim and execute it ---"
for _ in $(seq 1 30); do
  RESULT=$(curl -sf "$CP_URL/v1/environments/e2e/verifications/$VERIFICATION_ID" -H "Authorization: Bearer $TOKEN")
  STATUS=$(echo "$RESULT" | jq -r .status)
  [ "$STATUS" = "succeeded" ] || [ "$STATUS" = "failed" ] && break
  sleep 2
done

echo "$RESULT" | jq .
if [ "$STATUS" != "succeeded" ]; then
  echo "verification did not succeed (status=$STATUS)"
  exit 1
fi

FAILED_TIERS=$(echo "$RESULT" | jq -r '.tier_results | to_entries[] | select(.value.ok != true) | .key')
if [ -n "$FAILED_TIERS" ]; then
  echo "tiers failed to assume their role: $FAILED_TIERS"
  exit 1
fi

echo "--- e2e passed: worker self-registered, was granted an environment, and assumed all three tier roles via a real (emulated) sts:AssumeRole ---"
