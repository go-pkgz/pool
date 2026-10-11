---
worth: later
where: pool.go:newCompletionCtx
added: 2026-10-11
---
# newCompletionCtx is a standalone function with one method caller

`newCompletionCtx` is called only from `finishWorker`. Every other standalone function in the module is
exported package API, and the other internal helper struct, `workerRequest`, is built as a literal at
its call site. Either inline `completionCtx{Context: p.callerCtx, values: context.WithoutCancel(ctx)}`
in `finishWorker` or make it a `WorkerGroup` method taking only the worker context. The
`//nolint:contextcheck` directive above the call may have to move with it. Surfaced reviewing PR #13,
no behaviour effect.
