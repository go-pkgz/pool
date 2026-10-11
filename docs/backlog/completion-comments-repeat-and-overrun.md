---
worth: later
where: pool.go:completionCtx
added: 2026-10-11
---
# completion comments repeat the errgroup rationale and run long

The reason `completeErr` exists (errgroup keeps the first error only) is stated on the `completeErr`
field and again in the `waitWorkers` godoc, so a later edit has two places to keep in step. Keep it on
the field and cut the `waitWorkers` godoc to what the method does. The `completionCtx` godoc is four
lines on a four-line type and the `newCompletionCtx` godoc restates the signature; trim both when the
lines are touched anyway. The `Value` comment is accurate and should stay. Surfaced reviewing PR #13.
