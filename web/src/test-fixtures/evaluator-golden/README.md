# evaluator-golden

Hand-authored fixtures shared between the Go (`internal/labels`) and TS
(`web/src/features/publicprofile/labels/evaluator.ts`) label evaluators.

**Ticket:** gaka-hc6.2 — drift guard for evaluator parity.

Both evaluators load every `*.json` in this directory, evaluate the
`payload` against the `catalog`, and assert the resulting award ids
(sorted alphabetically) equal `expectedAwards`. If Go and TS disagree
on ANY fixture, one evaluator has a bug — file it as a blocker bead.

## Fixture shape

```json
{
  "name": "human-readable label",
  "payload": {
    "username": "...", "startDate": "...", "endDate": "...",
    "totalSeconds": 0, "dailyAvg": 0, "dailyTotal": [...],
    "languages": [{"name": "...", "totalSeconds": 0, "totalPct": 0, ...}],
    "editors": [...], "projects": [...], "categories": [...],
    "platforms": [...],
    "punchcard": {"cells": [{"dow": 0, "hour": 0, "seconds": 0}],
                  "maxSeconds": 0, "totalSeconds": 0}
  },
  "catalog": [
    {
      "id": "...",
      "kind": "tier|archetype|tribe|meme|patch",
      "label": "...",
      "glyph": "",
      "description": "",
      "rank": 100,
      "tier": "",
      "tierKey": "",
      "condition": { "kind": "axis-time", ... }
    }
  ],
  "expectedAwards": ["id-1", "id-2"]
}
```

`expectedAwards` MUST be sorted alphabetically — the drift test compares
sorted lists so the order the evaluator returns is not asserted here (that
is covered by unit tests in each language separately).

## Adding a fixture

Keep them SMALL: one axis, one or two labels, intent readable at a glance.
Threshold-inclusive cases (value == threshold) are especially valuable.
