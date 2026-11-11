# Engine compatibility baseline

`engine-0.27.0.json` freezes the bundled engine contract from commit
`47870bd4be4c37a61b934237f39a8a4ef46867f7` (`api/openapi.cli.json`).
CLI-only `x-cli-ignore` flags were removed and the known invalid
`Engine.apiVersion` enum was corrected to the semantic version pattern used by
runtime `0.27.0`. No operation, input, status or other schema was changed.
This explicit erratum prevents preserving a schema that already rejected its
own deployed engine response. Original source remains available at that commit.

Run `make check-api-compatibility`. Pinned oasdiff v1.37.0 checks breaking changes
against this baseline; `tests/contracts/test_compatibility.py` proves the gate
rejects deletion of an operation and a required response field while accepting
an additive optional response property. Runtime HTTP tests validate emitted JSON
and deliberately corrupted responses. These are complementary checks, not a
claim that schema comparison verifies runtime effects or all HTTP semantics.
Do not refresh the baseline automatically when changing the contract.
