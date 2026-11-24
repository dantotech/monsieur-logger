# Log-loss analysis in Azure Data Explorer (ADX)

Monsieur Logger writes three kinds of records (field `kind`):

| kind          | when                    | key fields                                                                    |
|---------------|-------------------------|-------------------------------------------------------------------------------|
| `run_start`   | once, when a run starts | `run_id`, `total_expected`, `expected_by_level`, run parameters               |
| `test`        | every generated line    | `run_id`, `seq` (1..N, gap-free), `level`, `level_seq`, `total_expected`      |
| `run_summary` | once, when a run ends   | `run_id`, `state`, `emitted`, `emitted_by_level`, `actual_logs_per_second`    |
| `app`         | the app's own logs      | `message`                                                                     |

`seq` is the counter and `run_id` the flag that differentiates test runs.
If nothing was lost, `count() == dcount(seq) == max(seq) == emitted` of the run summary.

The queries below assume Fluent Bit ships the container log line into a table
`FluentBitLogs` with the original JSON line in a `dynamic` column `log`.
Adapt the first `let` to your table/column layout.

```kusto
let ML = FluentBitLogs
    | extend j = todynamic(log)              // or: parse_json(log) if it is a string
    | where j.app == "monsieur-logger"
    | project ingestion_time(), j;
```

## Loss / duplicate summary per run

```kusto
let ML = FluentBitLogs | extend j = todynamic(log) | where j.app == "monsieur-logger";
let tests = ML
    | where j.kind == "test"
    | summarize received = count(),
                unique_seq = dcount(tolong(j.seq), 4),
                max_seq = max(tolong(j.seq)),
                first_ts = min(todatetime(j.timestamp)),
                last_ts = max(todatetime(j.timestamp))
      by run_id = tostring(j.run_id);
let summaries = ML
    | where j.kind == "run_summary"
    | project run_id = tostring(j.run_id),
              state = tostring(j.state),
              emitted = tolong(j.emitted),
              total_expected = tolong(j.total_expected),
              pod = tostring(j.pod), node = tostring(j.node);
summaries
| join kind=fullouter tests on run_id
| project run_id = coalesce(run_id, run_id1), state, node, emitted, total_expected,
          received, unique_seq, max_seq,
          lost = emitted - unique_seq,
          duplicates = received - unique_seq,
          loss_pct = round(100.0 * (emitted - unique_seq) / emitted, 4),
          first_ts, last_ts
| order by first_ts desc
```

> `dcount(..., 4)` is the most accurate HyperLogLog level but still an
> estimate for very large runs. For an exact number on huge runs use
> `summarize by seq | count` or the gap query below.

## Find the missing sequence numbers of one run

```kusto
let run = "fb-test-001";
let ML = FluentBitLogs | extend j = todynamic(log) | where j.app == "monsieur-logger" and j.run_id == run;
let expected = toscalar(ML | where j.kind == "run_summary" | project tolong(j.emitted));
range seq from 1 to expected step 1
| join kind=leftanti (ML | where j.kind == "test" | project seq = tolong(j.seq)) on seq
| summarize missing = count(), ranges = make_list(seq, 1000)
```

## Gaps as ranges (cheaper for big runs)

```kusto
let run = "fb-test-001";
FluentBitLogs
| extend j = todynamic(log)
| where j.app == "monsieur-logger" and j.kind == "test" and j.run_id == run
| distinct seq = tolong(j.seq)
| order by seq asc
| extend prev = prev(seq)
| where isnotnull(prev) and seq - prev > 1
| project gap_from = prev + 1, gap_to = seq - 1, missing = seq - prev - 1
```

## Loss per level

```kusto
let run = "fb-test-001";
let ML = FluentBitLogs | extend j = todynamic(log) | where j.app == "monsieur-logger" and j.run_id == run;
let expected = ML | where j.kind == "run_summary"
    | mv-expand kind=array lv = j.emitted_by_level
    | project level = tostring(lv[0]), emitted = tolong(lv[1]);
ML | where j.kind == "test"
| summarize received = dcount(tolong(j.level_seq), 4) by level = tostring(j.level)
| join kind=rightouter expected on level
| project level = coalesce(level, level1), emitted, received, lost = emitted - received
```

## End-to-end latency (log timestamp -> ADX ingestion)

```kusto
FluentBitLogs
| extend j = todynamic(log)
| where j.app == "monsieur-logger" and j.kind == "test" and j.run_id == "fb-test-001"
| extend latency = ingestion_time() - todatetime(j.timestamp)
| summarize p50 = percentile(latency, 50), p95 = percentile(latency, 95), p99 = percentile(latency, 99), max(latency)
```
