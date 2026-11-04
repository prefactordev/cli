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

### Global flags

| Flag | Environment variable | Effect |
|------|----------------------|--------|
| `--json` | `PREFACTOR_OUTPUT_FORMAT=json` | Print JSON on every command, including `version`, `profiles` and `login` (whose instructions then go to stderr) |
| `--fields <a,b.c>` | | Keep only these fields of the JSON output. Paths continue through arrays, so `summaries.id` keeps the id of every summary. Implies `--json` |
| `--no-input` | `PREFACTOR_NO_INPUT=1` | Never prompt |
| `--yes` | | Accept the default answer to every prompt |

The CLI prompts only when stdin and stdout are terminals and no coding agent is detected (`CLAUDECODE`, `CURSOR_AGENT`, `GEMINI_CLI`, `CODEX_SANDBOX`, `CODEX_CI`, `AGENT` or `AI_AGENT` is set). Otherwise a command that needs missing input fails at once and names the flag to pass, so an agent never waits on a prompt.

### Pickers and `@latest`

In interactive mode, a command missing a required resource ID shows a list to choose from (type `/` to search), for example the agents for `prefactor agent-instances list`. Resources that belong to another, such as an agent's instances, ask for the parent first unless the command already has it. A list with one entry is used without asking. Pickers draw on stderr, so stdout keeps the command's output.

`@latest` stands for the most recent instance of an agent wherever an agent instance ID is accepted: `prefactor agent-instances retrieve @latest`. The agent comes from the command's `--agent-id`, or from a picker in interactive mode.

Outside interactive mode, or with `--yes`, there are no pickers; a missing ID fails with the argument or flag to pass.

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
