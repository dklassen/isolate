---
name: data-pipeline-standards
description: Standards and checklist for building ingestion and transformation pipelines. Apply this whenever building, reviewing, or extending a data pipeline (ingestion, transformation, or loading step), regardless of language or orchestration tool. Covers data discovery/profiling before design, the questions that must be clarified, the tradeoffs that must be consciously decided, and the engineering practices that are expected by default.
---

# Data Pipeline Standards

## Engineering Best Practices

- **Idempotency**: rerunning any step must not duplicate or corrupt data. Writes (upserts, dedup keys) should be designed with this in mind from the start.
- **Separation of concerns**: ingestion, validation, and transformation are kept as distinct steps/functions.
- **Testability**: at least one test or a sample-data dry run per step is required before a step is considered done.
- **Observability**: enough should be logged at each step that a failure indicates where and why it broke, not just that something broke.
- **Config over hardcoding**: paths, thresholds, and connection info are passed as parameters/config, not embedded in logic.
- **Reviewer-readable**: naming and structure should let someone unfamiliar follow the pipeline from the PR alone.

## Before anything is designed, clarify

If these are not already answered, they should be asked (or, if asking isn't possible, an assumption should be stated explicitly):

- **Source/sink**: what is the data coming from and going to? Files, API, queue, table?
- **Volume/scale**: rough rows/sec or GB, batch or streaming?
- **Schema stability**: fixed schema, or does it evolve? Any nullable/nested fields expected?
- **Failure semantics**: is at-least-once acceptable, or is exactly-once / dedup required?
- **Ordering**: does downstream care about order, or is it commutative?
- **Definition of "correct"**: must a rerun be safe (idempotent)? Is partial failure acceptable if logged?

## Discover and understand the data before building

Before any design or code, the data itself must be inspected — not assumed. This step is not optional, even under time pressure.

- **Confirm the use case, not just the schema**: what will this data be used for downstream, and what does that imply about what "correct" and "complete" mean? What are the current definitions if any for incoming fields.
- What is the
- **Sample the actual data**: pull real (or representative sample) records, not just a schema doc or spec.
- **Profile the shape**: field types, null rates, cardinality, value ranges, and any fields that look like they encode more than one thing (e.g., a "status" field that's really three concerns bundled together).
- **Look for what breaks assumptions**: duplicate records, out-of-order timestamps, inconsistent formats within the same field, unexpected nulls, encoding issues.
- **State findings before designing**: a short summary of what the data actually looks like (and where it surprised expectations) should be given before moving to design — this is what design decisions in the next section should be grounded in, not guesses.

## Tradeoffs that must be consciously decided (and narrated)

- **Batch vs streaming** — chosen based on stated latency needs, not habit. If unstated, batch is the default, and the reason should be stated.
- **At-least-once + idempotent writes** vs **exactly-once semantics** — the former is simpler and usually sufficient; the latter should only be reached for if duplicates are genuinely unacceptable, since it adds real complexity (transactional writes, dedup keys).
- **Fail-fast vs skip-and-quarantine** — it must be decided, per pipeline, whether one bad record kills the batch or gets logged/quarantined while the rest proceeds. This is usually the single highest-leverage design decision in an ingestion step.
- **Schema-on-write vs schema-on-read** — strict validation at ingestion (fail fast, clean data downstream) vs. permissive ingestion with validation later (more resilient, more junk to handle downstream).
- **Simplicity vs extensibility** — abstraction should not be built for hypothetical future requirements. The simplest structure that meets the stated scope is the default, and any decision not to generalize should be stated explicitly.

## Working style expected of the agent

- Work should proceed in small, scoped steps — one stage (e.g., ingestion) working on sample data before the next is started.
- Every diff must be reviewed before being run. If something is missed (null handling, retry logic, schema drift), it should be flagged and corrected explicitly — not silently accepted as working-but-incomplete.
- Assumptions and tradeoffs should be narrated as they're made, even when not asked — the reasoning is as much the deliverable as the code.

## Still to refine

(placeholder — update this file as iteration continues)
