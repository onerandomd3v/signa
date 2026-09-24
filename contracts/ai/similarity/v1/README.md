# Same-event similarity v1

The v1 output is one assessment per supplied candidate incident. Its score is
an uncalibrated arithmetic mean of informative factor values only; it is not a
probability, incident confidence, truth/verification, or attach threshold.
Unknown and ambiguous time/location evidence remains indeterminate and does
not contribute a negative score. Text-only location comparison recognizes
exact normalized wording only. Spatial proximity is used only when the
approved candidate lookup supplies both distance and its query radius; this
component does not geocode or calculate coordinates. No persistence,
attachment, incident creation, severity, or alert decision is made.

`evaluation.json` versions the fixture suite and exact numeric/status
tolerances. Run it with:

```powershell
go test ./internal/ai/similarity/evaluation -run '^TestVersionedV1SimilaritySuite$' -count=1 -v
```
