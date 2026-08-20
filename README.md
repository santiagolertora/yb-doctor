# yb-doctor

CLI that diagnoses a YugabyteDB universe from YB-Master HTTP, TServer Prometheus, and (optionally) YSQL.

It reports tablet placement, leader skew, under-replicated / leaderless tablets, DocDB SST and compaction, allowlisted TServer gflags that differ from default, and whether Raft still has quorum if you lose a node, an AZ, or a region.

On YugabyteDB 2025.2, TServer `:9000` has no `SQLProcessor_SelectStmt`. Pass `--ysql host:5433` to read `pg_stat_statements` for P99 and `yb_tablet_metadata` for tablets.

```text
$ yb-doctor analyze --scenario scenarios/leader-imbalance --no-color

YugabyteDB Cluster Health
─────────────────────────────────────────────
Nodes                    6
Masters                  3/3 healthy
TServers                 6/6 healthy
Replication Factor       3
Regions                  aws.eu-west-1

Tablet Health
─────────────────────────────────────────────
Tablets                  1,284
Under-replicated         0
Leader imbalance         WARNING
Tablet imbalance         OK

Node Distribution
─────────────────────────────────────────────
                    Leaders   Followers    Total      SST  Pending   Debt
yb-01                   312         331      643      10G      n/a     0%
yb-02                   305         337      642      10G      n/a     0%
yb-03                   301         342      643      12G     2.0G    17%
yb-04                   121         518      639      10G      n/a     0%
yb-05                   119         521      640      10G      n/a     0%
yb-06                   126         519      645      10G      n/a     0%

WARNING: Leader imbalance detected
  yb-01 has 2.6x more leaders than yb-05.

Raft
─────────────────────────────────────────────
Leaderless tablets       0
Under-replicated         0
Slow followers           7

Performance
─────────────────────────────────────────────
P99 YSQL latency         38ms
Slow queries             12
Hot tablets              3
Compaction pressure      HIGH on yb-03

Diagnosis
─────────────────────────────────────────────

[HIGH] Compaction pressure on yb-03
       → pending compaction: 2.0 GiB
       → SST files: 12.0 GiB
       → pending/SST: 17%
       → disk utilization: 87%
       → write latency correlated +64%

[MEDIUM] Leader imbalance
       → consider checking leader balancing

Overall score: 78/100
```

That sample is the six-node fixture. Point `--master` at a live Master (and `--ysql` at 5433) for the same report from a real universe.

| Command | Does |
| --- | --- |
| `analyze` | Topology, tablets, Raft, SST/compaction, findings, score. `--out` / `--diff` for before-after. `--watch 30s` while an AZ dies. |
| `resilience` | Drop a node / AZ / region and check `RF/2+1` on remaining voters |
| `explain <code>` | WHAT / WHY / this cluster / what to check next |
| `poc` | Pass/fail against `scenarios/poc-criteria.json` (needs `--ysql` for a real P99) |

```bash
yb-doctor explain --list
```

## Install

Go 1.24+.

```bash
go install github.com/santiagolertora/yb-doctor/cmd/yb-doctor@latest
```

```bash
git clone git@github.com:santiagolertora/yb-doctor.git
cd yb-doctor
task build
./dist/yb-doctor version
```

## Live cluster

Master HTTP defaults to port 7000:

```bash
yb-doctor analyze --master 127.0.0.1:7000
yb-doctor resilience --master 127.0.0.1:7000
yb-doctor explain leader-imbalance --master 127.0.0.1:7000
yb-doctor poc --master 127.0.0.1:7000 --criteria scenarios/poc-criteria.json
```

`--format json` works on every command. `--no-color` or `NO_COLOR=1` when piping.

Connection settings: `--flag` wins, then env (`YB_DOCTOR_*`), then a TOML file (`--config`, `YB_DOCTOR_CONFIG`, or `./yb-doctor.toml` if present). Copy [yb-doctor.toml.example](yb-doctor.toml.example). The password is never logged; prefer the file or `YB_DOCTOR_YSQL_PASSWORD` over `--ysql-password` (it shows in `ps`).

```bash
yb-doctor analyze --config ./yb-doctor.toml
yb-doctor analyze --master 127.0.0.1:7000 \
  --ysql 127.0.0.1:5433 --ysql-user yugabyte --ysql-sslmode disable
```

Yugabyte Aeon exposes YSQL `:5433` with TLS, not Master HTTP `:7000`. `analyze` still needs `--master` (or `masters` in the TOML) until that host is reachable.

`--ysql host:5433` reads `pg_stat_statements` for P99 and `yb_tablet_metadata` for the tablet map. `yb_servers()` runs only when Master HTTP has no placement. Each call has its own connection and timeout (`YB_DOCTOR_YSQL_TIMEOUT`, default 15s).

```bash
yb-doctor analyze --master 127.0.0.1:7000 --ysql 127.0.0.1:5433 --out before.json
docker stop yugabyte-n3
yb-doctor analyze --master 127.0.0.1:7000 --ysql 127.0.0.1:5433 --diff before.json
yb-doctor analyze --master 127.0.0.1:7000 --watch 30s --watch-interval 3s
```

`--diff` prints score, Master/TServer counts, per-node leaders, and findings that appeared or went away. `--watch` re-collects until the duration elapses, then diffs the first sample against the last.

Collector:

- `/api/v1/is-leader`, `/api/v1/masters`, `/api/v1/tablet-servers`
- `/api/v1/cluster-config`
- `/api/v1/health-check`, `/api/v1/tablet-replication` (optional)
- `/dump-entities` or `/api/v1/dump-entities`
- `/api/v1/varz` (`enable_load_balancing`) and `/api/v1/is-load-balancer-idle` when the Master has it
- Master `/prometheus-metrics` (`is_load_balancing_enabled`)
- TServer `/prometheus-metrics` (`quantile="p99"` as well as `"0.99"`; SST and pending compaction summed per tablet, OpenMetrics timestamps ignored)
- TServer `/api/v1/varz` (allowlisted gflags only, attached to findings when they differ from default)
- YSQL, if `--ysql` is set

If leaders are skewed, the finding says whether Master load balancing is enabled, idle, still running, or idle-unknown on this version.

Node table: Leaders / Followers / Total / SST / Pending / Debt. Compaction warns only when pending is at least 1 GiB and pending/SST is at least 10% (or SST is empty). SST imbalance warns when the max/min SST ratio among alive nodes is at least 3.

## Scenarios (no cluster)

Same analyzers, fixture snapshot. Useful on a laptop or in CI.

```bash
yb-doctor analyze --scenario scenarios/leader-imbalance
yb-doctor resilience --scenario scenarios/healthy
yb-doctor explain compaction-pressure --scenario scenarios/leader-imbalance
yb-doctor poc --scenario scenarios/healthy --criteria scenarios/poc-criteria.json
```

| Path | Contents |
| --- | --- |
| `scenarios/healthy` | 3 AZs, RF=3, even leaders. Node/AZ loss passes, region loss fails. |
| `scenarios/leader-imbalance` | 6 TServers, leaders piled on yb-01, compaction on yb-03 |
| `scenarios/node-failure` | Dead TServer, under-replicated tablets |
| `scenarios/az-failure` | Two AZs, cannot survive losing one |
| `scenarios/compaction-pressure` | DocDB compaction backlog, disk > 90% |

## Docker lab

Three nodes, RF=3, one AZ each (`eu-west-1a/b/c`). Containers: `yugabyte-n1`, `yugabyte-n2`, `yugabyte-n3`, label `com.yugabyte.product=yugabytedb`.

Compose pins `yugabytedb/yugabyte:2025.2.5.2-b5-aarch64` / `linux/arm64`. Hub `latest` is amd64.

Host ports (7000/9000 are often already taken):

| Host | Inside | What |
| --- | --- | --- |
| 27000–27002 | 7000 | Master HTTP |
| 28000–28002 | 9000 | TServer HTTP |
| 26433–26435 | 5433 | YSQL |

```bash
task lab:up        # compose up, wait for Masters, zone placement
task lab:load      # hash-split tables + ~200k rows
task lab:analyze   # --master 127.0.0.1:27000
task lab:resilience
task lab:poc
task lab:ps
task lab:down
```

```bash
yb-doctor analyze --master 127.0.0.1:27000 --ysql 127.0.0.1:26433 --out before.json
docker stop yugabyte-n3
yb-doctor analyze --master 127.0.0.1:27000 --ysql 127.0.0.1:26433 --diff before.json
yb-doctor analyze --master 127.0.0.1:27000 --ysql 127.0.0.1:26433 --watch 30s --watch-interval 3s
docker start yugabyte-n3
```

`task lab:analyze` / `lab:poc` set `YB_DOCTOR_YSQL=127.0.0.1:26433` and `YB_DOCTOR_TSERVER_HTTP_BASE=28000`.

## Where things live

```text
cmd/yb-doctor          main
internal/app           analyze, resilience, explain, poc
internal/domain        Snapshot / tablets / Raft types
internal/adapter/...   CLI, Master HTTP, YSQL, scenario files
```

Diagnosis runs on a `Snapshot`. Collectors fill it; analyzers do not open sockets. Same findings from a scenario JSON or a live Master.

```bash
task test
task test:integration
task lint
task cover    # fails under 80% on ./internal/...
task build
```

[BSL 1.1](LICENSE) until 2030-08-13, then Apache 2.0. Use it on clusters you run or support. Do not ship it as a hosted diagnostic product.
