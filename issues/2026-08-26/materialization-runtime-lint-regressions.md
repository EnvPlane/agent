# Materialization runtime lint regressions

## Problem

The materialization command transport introduced unchecked HTTP response closes,
an overwritten execution error, and a non-idiomatic method switch in its test.
The required golangci-lint job therefore fails.

## Resolution

Ignore close errors explicitly, preserve the materializer execution error before
wire conversion, and use a tagged method switch in the test server.
