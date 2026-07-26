# Session 20 — Opportunity evaluation lab

## Status

Research backlog. It is intentionally excluded from Sessions 12–19 metrics and support claims.

## Purpose

Define whether a skill was eligible but not selected without using vague model judgment, persisting
raw prompts or presenting inference as exact runtime evidence.

## Definitions

- **Eligible:** a versioned rule or controlled local classifier matched the task while the skill was
  enabled and exposed.
- **Selected:** exact invocation evidence exists.
- **Missed:** eligible and not selected inside a complete observation window.
- **False positive:** reviewed evaluation evidence says the eligibility result was incorrect.

The classifier runs locally and ephemerally. Durable output is limited to rule/classifier ID and
version, boolean result, bounded reason category, confidence, inventory identity and evidence
lineage. Prompt text and features capable of reconstructing it are prohibited.

## Research questions

- Which skill trigger contracts are deterministic enough to evaluate?
- Can eligibility be computed from user-provided labels rather than prompt content?
- How are false positives reviewed without retaining tasks?
- What minimum sample and completeness are required for activation recall?
- How does the classifier budget affect latency, privacy and local resource use?

## Exit gate before implementation

A threat model, controlled sanitized corpus, false-positive review protocol, privacy canary and
formula contract are approved. Until then the UI omits opportunity counts instead of showing zero,
unknown or a speculative score.
