# Agent Contract Matrix

WarpLocal runs one framework-neutral test suite against every bundled Agent.
The suite is defined in `cmd/server/agent_contract_matrix_test.go`.

## Required common scenarios

- A plain Turn streams assistant output and completes.
- A later Turn in the same Conversation retains completed history.
- Different Conversations do not share history.
- A Conversation accepts another Turn after the previous Turn completes.

The tests use a deterministic local model endpoint. Native runs through the
real Adapter request handler. Pi and DeepSeek Harness run through their real
Node Sidecars and the production NDJSON process driver.

## Adding a framework

1. Add its driver name to `config.BundledAgentDrivers`.
2. Register a real runner in `contractFactories`.
3. Build its runtime before running the matrix.

The registry test fails if either list is missing an entry. The App bundling
script runs the matrix after building Sidecars, so an incompatible framework
cannot silently enter a package.

## Related shared-layer coverage

Tool batches, rejection, timeout, foreground/background execution, polling,
input, cancellation, Steer, and real local SSH are Adapter/client execution
contracts. They remain shared-layer tests because the Sidecars emit the same
normalized protocol; duplicating terminal mechanics per model framework would
test the wrong boundary.
