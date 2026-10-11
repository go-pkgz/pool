---
worth: later
where: pool_test.go:TestPool_PoolCompletion
added: 2026-10-11
---
# no test runs the completion callback after the caller's deadline expires

README and CLAUDE.md both promise the pool completion callback still runs on
`context.DeadlineExceeded`, and the gate in `finishWorker` does that by skipping only on
`context.Canceled`. Nothing tests it: the existing `DeadlineExceeded` assertions are `Close`/`Wait`
timeout tests with no completion callback. Simplifying the gate to `p.callerCtx.Err() == nil` would
pass the whole suite and leave a chained pool's next stage unclosed when the caller's deadline
expires. Wanted: a subtest with `context.WithTimeout` on the `Go` context and a worker blocking on
`ctx.Done()`, asserting the callback ran and `Close` returns `context.DeadlineExceeded`. Predates PR
#13, surfaced reviewing it.
