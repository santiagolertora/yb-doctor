#!/usr/bin/env bash
# Load a POC-shaped YSQL workload into the local yb-doctor lab, then
# generate a burst of reads so TServer Prometheus histograms have P99.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
SQL="${ROOT}/build/ci/demo-workload.sql"
LOOPS="${1:-40}"

ysql() {
  local node="$1"
  shift
  docker exec -i "${node}" bin/ysqlsh -h "${node}" -U yugabyte -d yugabyte "$@"
}

echo "== schema + bulk load (orders/events/sessions) =="
ysql yugabyte-n1 -f - <"${SQL}"

echo "== row counts =="
ysql yugabyte-n1 -c "SELECT 'demo_orders' AS t, count(*) FROM demo_orders
UNION ALL SELECT 'demo_events', count(*) FROM demo_events
UNION ALL SELECT 'demo_sessions', count(*) FROM demo_sessions;"

echo "== mixed read/write burst (${LOOPS} rounds × 3 nodes) =="
for i in $(seq 1 "${LOOPS}"); do
  ysql yugabyte-n1 -c "SELECT count(*) FROM demo_orders WHERE customer_id BETWEEN 10 AND 80;
SELECT * FROM demo_orders ORDER BY created_at DESC LIMIT 20;
UPDATE demo_sessions SET payload = repeat('hot', 40) WHERE seq % 200 = ${i};" >/dev/null
  ysql yugabyte-n2 -c "SELECT count(*) FROM demo_events WHERE kind = 'purchase';
SELECT * FROM demo_sessions WHERE bucket = 1 ORDER BY seq DESC LIMIT 50;" >/dev/null
  ysql yugabyte-n3 -c "SELECT count(*) FROM demo_orders WHERE amount > 200;
SELECT * FROM demo_events WHERE user_id % 17 = 0 LIMIT 30;" >/dev/null
  printf "."
done
echo
echo "== workload ready. next: task lab:analyze =="
