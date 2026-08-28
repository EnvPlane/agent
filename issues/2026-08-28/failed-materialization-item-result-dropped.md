# Failed materialization item result was dropped

The Agent materializer returns completed item results together with an error
when execution stops at the first failed item. The command runtime converted
those results to the shared wire DTO only when execution returned no error, so
the control plane received a failed command without the stable item-level error
code.

This made a foreign target Secret indistinguishable from other failures and
violated the resumable partial-failure contract.

## Resolution

Convert and report every available partial item result before propagating the
execution error to the command-level status.
