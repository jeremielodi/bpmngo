# BpmnGo compatibility improvements after the benchmark

The original Camunda 8.10.7 comparison found 35/47 strictly matching
observable scenarios. The implementation was then corrected and the exact
same 47-model suite was rerun against both engines.

## Corrected behavior

- Message start events no longer incorrectly enforce one active instance per
  process and correlation key. Distinct messages can start concurrent
  instances; `messageId`/TTL still provide publication deduplication.
- Explicit BPMN `<outgoing>` order is preserved for gateways, making the
  first-true exclusive-flow rule deterministic and Camunda-compatible.
- `/v2` uses the same int64 public keys as gRPC by default; UUID responses
  remain available with `REST_KEYS=uuid`.
- Message and signal publication responses now expose Camunda's
  `messageKey`/`signalKey` and `tenantId` shape.
- User-task completion now returns HTTP 204 with no body.
- Process instances, user tasks, incidents, element instances and variables
  now expose Camunda-compatible keys, metadata, values and pagination.
- Docker's build context excludes benchmark/cache artifacts (about 573 MB
  before, about 0.3 MB after).

## Verification

- Full Go regression suite: `go test -mod=vendor ./...` passes.
- BpmnGo behavior suite was repeated on the same database, including an
  earlier still-active message-start instance, and all 47 assertions passed.
- Updated identical-model reference:
  `raw/camunda-improved-reference.json`.
- Definitive BpmnGo output:
  `raw/bpmngo-improved-definitive.json`.
- Reproducible comparison:

  ```sh
  node scripts/compare-behavior.js \
    raw/camunda-improved-reference.json \
    raw/bpmngo-improved-definitive.json
  ```

That post-fix validation produced `IDENTICAL_NORMALIZED 47/47`. Normalization covers only
engine-generated keys, timestamps, opaque cursors, deployment versions and
the scheduling-dependent measured backoff duration; nullability, response
shape, status codes, job behavior and business values remain compared.

## Subsequent clean-volume rerun

The complete clean-volume rerun in [`REPORT-RERUN-20261029.md`](REPORT-RERUN-20261029.md)
supersedes the retained-database result for release readiness. It reproduced
one remaining defect twice: an immediate search of a newly finished instance
returned only its `end` element rather than `start`, `work`, and `end`.
Consequently the current clean result is **46/47**, despite the earlier
retained-database validation reaching 47/47.
