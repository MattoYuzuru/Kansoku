# Kansoku contract registries

These registries are the versioned product boundary established by Session 01. Files use the JSON
subset of YAML 1.2 so the bootstrap validator can run with the Python standard library only. Later
generators may emit native YAML without changing the schemas.

Run all contract checks with:

```sh
python3 scripts/validate_contracts.py
python3 -m unittest discover -s tests -v
```

Registry changes require a schema or formula version change, deterministic fixtures, and an update
to the relevant proposal/TDD. Existing entries in `formula-version-locks.yaml` are append-only after
their first trusted commit; semantic changes require a new version. A support label is a
capability-and-version claim, never a brand-wide claim. Supported and Beta both require an explicitly
parsed and ordered capability version range, typed evidence receipts bound to the exact
adapter/capability/range tuple and validator-recomputed canonical fixture bytes, and two independent
approved human review receipts bound to the same tuple and verified fixtures. Session sequencing
never bypasses that public-claim gate.
