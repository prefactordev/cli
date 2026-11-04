# Prefactor CLI

Command-line interface for [Prefactor](https://prefactor.tech).

## Commands

| Command | Description |
|---------|-------------|
| `prefactor login` | Sign in through the browser and save the token to the `default` profile |
| `prefactor profiles list` | List configured profiles |
| `prefactor profiles add <name> [baseUrl] --api-token <token>` | Add or update a profile |
| `prefactor profiles remove <name>` | Remove a profile |
| `prefactor ping [--api-token <token>] [--api-url <url>]` | Verify the selected or supplied API token |
| `prefactor pfid generate [--count <n>] [--account-id <id>]` | Generate Prefactor IDs |
| `prefactor version` | Print the CLI version |

Resource commands cover every operation in the Prefactor API, grouped by resource: `accounts`, `environments`, `agents`, `judges`, `agent-versions`, `agent-schema-versions`, `agent-deployments`, `agent-associations`, `agent-instance-associations`, `agent-instances`, `agent-spans`, `alerts`, `teams`, `people`, `risk-profiles`, `integrations`, `api-tokens`, `admin-users`, `admin-user-invites`, `llm-providers`, `llm-provider-models`, `knowledgebases`, `knowledgebase-documents`, `knowledgebase-document-revisions`, `knowledgebase-revisions` and `bulk`. Run `prefactor <group> --help` for the subcommands. JSON flags accept a JSON value or `@path/to/file.json`.

`--profile <name>` or `PREFACTOR_PROFILE` selects a profile. Without either, the `default` profile is used, and `PREFACTOR_API_TOKEN` and `PREFACTOR_API_URL` apply when it is not configured.

Profiles live in `prefactor.json`: first at the root of the git repository that contains the current directory, then in the install root (`~/.prefactor/prefactor.json`).

## Development

The Go toolchain and golangci-lint are pinned with [mise](https://mise.jdx.dev):

```sh
mise install
go build ./cmd/prefactor
go test ./...
golangci-lint run ./...
```

### API client

`api/openapi.json` is a copy of the p2 OpenAPI document. `internal/api/client.gen.go` is generated from it and committed:

```sh
go generate ./...
```

Generation first runs `tools/specnormalize`, which writes `api/openapi.normalized.json`. The p2 document describes most request bodies as `"in": "body"` parameters, which OpenAPI 3.0 does not allow, so the normaliser moves them into a JSON `requestBody` before `oapi-codegen` runs. CI fails when the committed generated files differ from a fresh `go generate`.

`api/coverage.yaml` maps every operation in the OpenAPI document to the command that calls it, or marks it excluded or pending. `go test ./...` fails when an operation is missing from it.

### Parity with the TypeScript CLI

`test/parity` runs the cases in `test/parity/cases/` through the TypeScript CLI and this CLI against a recording server, and compares requests, output and exit codes:

```sh
PARITY_TS_CLI="bun ~/code/typescript-sdk/packages/cli/src/bin/cli.ts" go test -tags parity ./test/parity/
```

Build the TypeScript SDK first (`bun run build` in that repository), because the TypeScript CLI runs against the built `@prefactor/core`.
