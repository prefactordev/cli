# API drift reference

## How the document is used

- `api/openapi.json` is a byte-for-byte copy of p2's `openapi.json`. Never edit it by hand.
- `go generate ./...` runs `tools/specnormalize` (which moves `"in": "body"` parameters into a JSON `requestBody`, as OpenAPI 3.0 requires) to write `api/openapi.normalized.json`, then `oapi-codegen` to write `internal/api/client.gen.go`. Both are committed; CI fails when they differ from a fresh `go generate`.
- `api/coverage.yaml` maps every `operationId` to the command that calls it, or marks it `excluded` (with the reason) or `pending` (with the issue that adds it).

## Quirks

- Most request bodies are parameters with `in: body`; only `POST /api/v1/bulk` uses `requestBody`. The digest prints both.
- Nullable is `oneOf: [T, {$ref: Null}]`; the digest prints `T | null`. A field without `?` in the digest is required.
- Path parameters have resource-specific names (`{agent_span_id}`). A renamed path parameter changes the generated client's function signature, so it shows as a build failure.
- Sort fields are listed in the `sorting` parameter's description (`Supported sort fields: ...`), so a description change there can change the `--sort-field` choices.
- Array query parameters are sent as `name[]=value` (`idListQuery` in `internal/cli/shared.go`), which p2 accepts though the document does not say so.

## What the tests check

| Failure | Meaning |
|---|---|
| `go build` fails in `internal/cli` | The generated client changed under a command: a renamed or removed operation, path parameter or type. Breaking drift. |
| `TestEveryOperationHasACommand` | A new operation with no entry in `api/coverage.yaml`. Coverage gap. |
| `TestCoverageListsOnlyOperationsInTheDocument` | An operation was removed or its `operationId` renamed. Breaking drift if a command calls it. |
| `TestEveryRequestFieldHasAnOption` | A new request field with no option, or an enum whose values differ from the option's choices. Coverage gap, or breaking drift when values were removed. |
| `TestRequestsMatchTheOpenAPIDocument` (`test/parity/contract_test.go`) | A request the CLI sends no longer validates: a new required field, a removed field, or a changed type. The case is in `test/parity/contract-cases/operations.txt`. Breaking drift. |
| `TestCommandTreeMatchesTypeScript` | An option the TypeScript CLI lacks no longer maps to a request field. Usually follows a removed field. |

## Responses the CLI reads

Most commands print the response as it comes, so a new response field needs nothing. The places that read fields by name, and so can break on a removed or renamed field:

- `internal/cli/setup.go`: agent, deployment and token responses (`details.id`, `token`, `environment_id`, and the ping response's `token_type` and `agent_id`).
- `internal/cli/login.go` and the device login decoding (`readDeviceLoginStart`).
- `internal/cli/resolve.go`, `resources.go`, `integrations.go` and `profiles.go`: `summaries`, `details`, `pagination`, and each item's `id` and label fields, for pickers, `@latest` and paging.
- `internal/human/`: the fields it chooses columns, statuses, times and timelines from (`labelKeys`, `statusKeys`, `classifierKeys`, `timeKeys`, `spans`). A renamed field only degrades the table, so it is a mismatch, not breaking.

Search for the field name to find any others.

## Keeping stage 1 green

Stage 1 lands the new document. To keep the tests passing without closing every gap at once:

- A new operation: add it to `api/coverage.yaml` as `pending` with the stage (or Linear issue) that adds its command.
- A new request field on an operation a command calls: add it to `fieldsWithoutOptions` in `internal/cli/request_fields_test.go`, with the stage that adds the option as the reason.
- A new required request field: add the option in stage 1, since the contract test fails without it.
- Breaking drift is fixed in stage 1.

Later stages remove these entries as they close the gaps.

## Recipes for closing gaps

**New command for a new operation:** add the command to the resource's file in `internal/cli/` (or a new file following an existing resource such as `agents.go`), with an option per request field via `fieldsQuery`/`fieldsBody` (`internal/cli/fields.go`), `addChoiceFlag` for enums and `addJSONFlag` for objects and arrays. Replace the `pending` entry in `api/coverage.yaml` with the command, add a valid invocation to `test/parity/contract-cases/operations.txt`, add the command to a section in `helpSections` (`internal/cli/help_root.go`) if it is a new group, and add a test in the resource's `_test.go`.

**New option for a new request field:** add the option, remove the field from `fieldsWithoutOptions`, and extend the operation's contract case if the field changes the body's shape.

**Help text:** update the command's `Short` or the option's usage. Commands and options the TypeScript CLI also has must keep its text (`TestCommandTreeMatchesTypeScript`); note those for the TypeScript CLI instead.

**Documented omission:** an operation the user decides to leave out is marked `excluded` in `api/coverage.yaml` with the reason, which later runs honour.
