# Copilot code review instructions for go-irc

## General Go guidelines

- Flag any use of `interface{}` or `any` where a concrete typed alternative exists.
- Every `error` return must be checked; flag ignored errors.
- Ensure exported functions, types, and constants have doc comments.
- Use table-driven tests for message parsing and protocol handling.

## Concurrency and network safety

- Flag goroutines that write to shared state without a mutex or channel synchronisation.
- Warn on `select` blocks with no `default` that could block indefinitely without a context deadline.
- Ensure all blocking network operations respect a context for cancellation.
- Flag unbounded buffers (channels, slices) used to accumulate messages from the network — prefer the ring buffer pattern already established in `internal/ringbuf`.
- Warn if a goroutine leak is possible: every spawned goroutine must have a clear termination condition.

## IRC protocol correctness

- Flag any raw string concatenation that builds IRC messages without using the existing message/command abstractions.
- Ensure numeric reply constants from `irc/numeric.go` are used rather than magic numbers.
- Warn on missing CRLF (`\r\n`) line endings when writing raw IRC lines.
- Flag mode string parsing that does not account for both `+` and `-` mode prefixes.

## Security (IRC-specific)

- Flag any password or credential logged at `DEBUG` level or higher.
- Ensure SASL authentication paths use `golang.org/x/crypto` and not a hand-rolled implementation.
- Warn on TLS configurations with `InsecureSkipVerify: true`.
- Flag bcrypt rounds below 12 in `server/bcrypt.go`.
- Ensure DCC file transfers validate the remote path before writing to disk (path traversal prevention).
