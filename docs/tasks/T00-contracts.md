# T00: Contracts, proto, shared error, import boundary test

**Status:** done · **Size:** S · **Depends on:** none · **Blocks:** T01, T04, T06

## Goal

Fix the shared seams so other tasks can run separately without colliding.

## Delivered

- `docs/tasks/`: this board, contracts, verified facts, one file per task.
- `proto/search/v1/search.proto` and generated code in `gen/search/v1/`.
- `make proto`, which regenerates the code.
- `domain.ErrUnavailable`, mapped to `503` with code `unavailable` in `internal/httpapi/errors.go`.
- `internal/archtest/boundary_test.go`, which enforces the import rule. Tested in both
  directions with a deliberate violation.
- `Dockerfile` copies `gen/`, which chat's image needs once T06 imports the generated code.

## Validate

```bash
make proto && git diff --stat gen/
make test
```

## Resume notes

Complete. See the board in `README.md` for what's next.
