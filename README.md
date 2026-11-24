<p align="center">
  <img src="assets/monsieur-logger.svg" alt="Monsieur Logger" width="240">
</p>

<h1 align="center">Monsieur Logger</h1>

<p align="center"><i>« Un log, deux logs, trois logs… aucun ne se perd. »</i></p>

Monsieur Logger is a small Go app that writes **JSON logs to stdout** at a
configurable rate, size, duration and level mix. It is built to test
**Fluent Bit on Kubernetes** and check whether any logs get lost on the way
to **Azure Data Explorer (ADX)**.

Every test line has a **`run_id`** to tell test runs apart and a gap-free
**`seq` counter**. Each run also writes a `run_start` and a `run_summary`
record with the expected and emitted counts, so ADX can show exactly how
many lines went missing and which ones.

## Features

- REST API with **Swagger UI** at `/swagger/` for starting, inspecting and stopping runs
- Parameters per run: `logs_per_second`, `log_length` (exact `message` size in bytes), `duration_seconds`
- Level mix via weights for **debug / info / warn / error** (e.g. `{"info":8,"warn":1,"error":1}`), with exact expected counts per level
- `run_id` and a gap-free `seq` per run, plus `level_seq` per level, `labels`, `pod`, `namespace`, `node`
- Several runs at once, graceful shutdown that still writes the `run_summary`
- Optional **auto-start** of a run when the pod starts (env / Helm values)
- Helm chart with **nodeSelector**, tolerations, affinity, and **requests/limits**
- Uses only the Go standard library; distroless, non-root, read-only image

## Log format

```json
{"timestamp":"2026-11-24T11:24:00.314857000Z","level":"info","app":"monsieur-logger","kind":"test","run_id":"smoke","seq":1,"level_seq":1,"total_expected":4000,"labels":{"scenario":"smoke"},"pod":"monsieur-logger-7d9c-abcde","namespace":"monsieur-logger","node":"aks-userpool-0","message_length":120,"message":"[seq=1] Bonjour Fluent Bit! Un croissant, une baguette et un cafe au lait, s'il vous plait. Le camembert est pret, la to"}
```

| field | meaning |
|---|---|
| `kind` | `test` (generated line), `run_start`, `run_summary`, `app` (the app's own logs) |
| `run_id` | identifies the test run (set it yourself or let the app generate one) |
| `seq` | 1..N with no gaps within a run, so a missing number means a lost line |
| `level_seq` | counter per level within a run |
| `total_expected` | `logs_per_second * duration_seconds` (0 = run until stopped) |
| `message` | exactly `log_length` bytes long |

## API

| method | path | description |
|---|---|---|
| `POST` | `/api/v1/runs` | start a run |
| `GET` | `/api/v1/runs` | list runs |
| `GET` | `/api/v1/runs/{id}` | status (emitted, rate, …) |
| `DELETE` | `/api/v1/runs/{id}` | stop a run (writes `run_summary`) |
| `GET` | `/api/v1/info` | version, pod/node, limits |
| `GET` | `/swagger/` | Swagger UI ([OpenAPI spec](api/openapi.yaml)) |
| `GET` | `/healthz`, `/readyz` | probes |

```bash
curl -X POST localhost:8080/api/v1/runs -H 'Content-Type: application/json' -d '{
  "run_id": "fb-test-001",
  "logs_per_second": 1000,
  "log_length": 512,
  "duration_seconds": 300,
  "levels": {"debug": 0, "info": 8, "warn": 1, "error": 1},
  "labels": {"scenario": "baseline"}
}'
```

Defaults: `logs_per_second=10`, `log_length=256`, `duration_seconds=0` (until stopped), all levels with weight 1.

> The Swagger UI page loads its JS/CSS from unpkg.com, so the **browser** needs internet access. The API itself does not.

## Configuration (environment)

| variable | default | description |
|---|---|---|
| `HTTP_ADDR` | `:8080` | listen address |
| `LOG_LEVEL` | `info` | level of the app's own logs (`kind=app`) |
| `MAX_CONCURRENT_RUNS` | `5` | |
| `MAX_LOGS_PER_SECOND` | `100000` | |
| `MAX_LOG_LENGTH` | `1048576` | |
| `MAX_DURATION_SECONDS` | `604800` | |
| `POD_NAME`, `POD_NAMESPACE`, `NODE_NAME` | | added to every line (the Helm chart sets them) |
| `AUTOSTART_ENABLED` | `false` | start a run at boot |
| `AUTOSTART_RUN_ID` | generated | |
| `AUTOSTART_LOGS_PER_SECOND` / `_LOG_LENGTH` / `_DURATION_SECONDS` | `10` / `256` / `60` | |
| `AUTOSTART_LEVELS` | all `1` | e.g. `debug=1,info=8,warn=1,error=0` |
| `AUTOSTART_LABELS` | | JSON object, e.g. `{"scenario":"boot"}` |

## Run locally

```bash
make run          # builds and starts on :8080, then open http://localhost:8080/swagger/
make test
```

## Deploy to Kubernetes

```bash
make docker-build docker-push IMAGE=<your-registry>/monsieur-logger
helm upgrade --install monsieur-logger deployments/helm/monsieur-logger \
  -n monsieur-logger --create-namespace \
  --set image.repository=<your-registry>/monsieur-logger --set image.tag=<tag> \
  -f deployments/helm/monsieur-logger/values-example.yaml

kubectl -n monsieur-logger port-forward svc/monsieur-logger 8080:8080
```

Pin the pod to a node and set its resources in the values:

```yaml
nodeSelector:
  kubernetes.io/hostname: aks-userpool-12345678-vmss000002
resources:
  requests: { cpu: 250m, memory: 64Mi }
  limits:   { cpu: "1",  memory: 256Mi }
```

The chart deploys a single pod (Deployment with `replicas: 1` and the `Recreate`
strategy, so two loggers never overlap during an upgrade). It adds the
annotation `fluentbit.io/parser: json` to the pod. See
[values.yaml](deployments/helm/monsieur-logger/values.yaml) for all options.

## Log-loss analysis in ADX

See [docs/adx-queries.md](docs/adx-queries.md) for KQL to get loss and duplicates per run, the missing `seq` ranges, loss per level and ingestion latency.

Tips for meaningful tests:
- Use a **new `run_id` per test**. Runs are kept in memory only, so a restarted pod forgets old IDs.
- A pod restart during a run means the run gets no `run_summary`. Compare against `total_expected` from `run_start` instead.
- When stdout is under back-pressure, the actual rate can drop below the target. Check `actual_logs_per_second` in the summary.
- Many runtimes split lines over 16 KiB (containerd/CRI `P` partial lines). Use large `log_length` values to test Fluent Bit's multiline/partial handling.

## Project layout

Follows [golang-standards/project-layout](https://github.com/golang-standards/project-layout):

```
api/                         OpenAPI spec (embedded)
assets/                      logo
build/package/               Dockerfile
cmd/monsieur-logger/         main
deployments/helm/            Helm chart
docs/                        ADX/KQL guide
internal/applog/             app's own JSON logger
internal/config/             env configuration
internal/generator/          run manager and log generator
internal/logwriter/          synchronized buffered stdout writer
internal/server/             HTTP API
internal/version/            build info
scripts/                     helper scripts
web/swagger/                 Swagger UI page (embedded)
```
