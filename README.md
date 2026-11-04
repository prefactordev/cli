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

Resource commands cover the operations in the Prefactor API, grouped by resource. `api/coverage.yaml` lists the operations that have no command yet (pending) or are left out on purpose (excluded). The groups are `accounts`, `environments`, `agents`, `judges`, `agent-versions`, `agent-schema-versions`, `agent-deployments`, `agent-associations`, `agent-instance-associations`, `agent-instances`, `agent-spans`, `alerts`, `teams`, `people`, `risk-profiles`, `integrations`, `api-tokens`, `admin-users`, `admin-user-invites`, `llm-providers`, `llm-provider-models`, `knowledgebases`, `knowledgebase-documents`, `knowledgebase-document-revisions`, `knowledgebase-revisions` and `bulk`. Run `prefactor <group> --help` for the subcommands. JSON flags accept a JSON value or `@path/to/file.json`.

### Global flags

| Flag | Environment variable | Effect |
|------|----------------------|--------|
| `--json` | `PREFACTOR_OUTPUT_FORMAT=json` | Print JSON from every command except `--version`/`-V`, including `version`, `profiles` and `login` (whose instructions and token prompt then go to stderr). The variable only sets the default, so `--content-only` still prints plain content |
| `--fields <a,b.c>` | | Keep only these fields of the JSON output. Paths continue through arrays, so `summaries.id` keeps the id of every summary. Implies `--json` |
| `--no-input` | `PREFACTOR_NO_INPUT=1` | Never prompt |
| `--yes` | | Accept the default answer to every prompt |

The CLI is interactive only when stdin and stdout are terminals and no coding agent is detected: none of `CLAUDECODE`, `CURSOR_AGENT`, `GEMINI_CLI`, `CODEX_SANDBOX`, `CODEX_CI`, `AGENT` or `AI_AGENT` is set to a value other than empty, `0`, `false`, `no` or `off`. Otherwise a command that needs missing input fails at once and names the flag to pass, so an agent never waits on a prompt.

### Pickers and `@latest`

In interactive mode, a command missing a required resource ID shows a list to choose from (type `/` to search), for example the agents for `prefactor agent-instances list`. Every other check runs first, so the CLI never asks you to pick for a command that would fail anyway. Resources that belong to another, such as an agent's instances, ask for the parent first unless the command already has it. A list with one entry is used without asking, except on commands that delete, revoke, terminate, archive, retire, suspend or discard data. Pickers draw on stderr, so stdout keeps the command's output; they show at most the newest 1000 entries.

There are no pickers outside interactive mode, when stderr is not a terminal, with `--yes`, or with JSON output (`--json`, `--fields` or `PREFACTOR_OUTPUT_FORMAT=json`); a missing ID then fails with the argument or flag to pass.

`@latest` stands for the instance of an agent with the highest ID, which is the newest one created, wherever an agent instance ID is accepted, including inside ID lists: `prefactor agent-instances retrieve @latest`. Scope it to an agent with `<agent_id>/@latest`, which works without a terminal. Without a scope, the agent comes from the command's `--agent-id`, or from a picker in interactive mode. Case does not matter.

`--profile <name>` or `PREFACTOR_PROFILE` selects a profile. Without either, the `default` profile is used, and `PREFACTOR_API_TOKEN` and `PREFACTOR_API_URL` apply when it is not configured.

Profiles live in `prefactor.json`, looked up as the TypeScript CLI does: first in the root of the git repository that contains the current directory (or the current directory outside a repository), then next to the binary, or in the parent of its directory when that directory is named `bin`. Symlinks to the binary are followed. The managed install at `~/.prefactor/bin/prefactor` therefore uses `~/.prefactor/prefactor.json`.

### Differences from the TypeScript CLI

Commands, flags and output match the TypeScript CLI, renamed to kebab case. These differences are deliberate:

- Requests send `sorting[field]`/`sorting[direction]` and `agent_ids[]`/`agent_instance_ids[]`, the forms p2 accepts. The TypeScript CLI sends `sorting=-name` and repeated `agent_ids=`, which p2 rejects.
- The `X-Prefactor-SDK` header is `prefactor-cli@<version>` rather than `@prefactor/core@<version>`.
- Errors that come from the runtime are worded by Go rather than Bun, for example invalid JSON in a flag, a missing `@file` and network failures.
- Pickers, `@latest`, `--json`, `--fields`, `--no-input` and `--yes` are new.

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
