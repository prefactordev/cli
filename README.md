# Prefactor CLI

Command-line interface for [Prefactor](https://prefactor.tech).

## Install

macOS and Linux:

```sh
curl -fsSL https://raw.githubusercontent.com/prefactordev/cli/main/install.sh | bash
```

Windows (PowerShell):

```powershell
irm https://raw.githubusercontent.com/prefactordev/cli/main/install.ps1 | iex
```

The installer downloads the release for your platform, checks it against the release's `SHA256SUMS`, and installs it to `~/.prefactor/bin/prefactor` (`%USERPROFILE%\.prefactor\bin\prefactor.exe` on Windows). It puts that directory on your `PATH`: `install.sh` adds it to your shell profile and links `prefactor` into a directory already on `PATH`, such as `~/.local/bin`, so it works at once; `install.ps1` adds it to your user `PATH`.

Pass `latest` for the newest build from `main`, or a version such as `0.7.0` to pin it:

```sh
curl -fsSL https://raw.githubusercontent.com/prefactordev/cli/main/install.sh | bash -s -- latest
```

Then manage the installation with the CLI:

| Command | Effect |
|---------|--------|
| `prefactor update [--channel stable\|latest] [--version <version>]` | Install the newest release of the channel you installed, or the one given. A pinned install stays on its version |
| `prefactor doctor` | Show where the CLI is installed, which release, and whether its directory is on `PATH` |
| `prefactor uninstall` | Remove the CLI, keeping your profiles in `~/.prefactor/prefactor.json` |

Both CLIs install to and read the same `~/.prefactor` files, and this CLI's `install` accepts the options the TypeScript CLI's `update` passes to the binary it downloads, so once the TypeScript CLI's last release updates from this repository, `prefactor update` moves a TypeScript installation over.

## First use

```sh
prefactor login                       # sign in through the browser
prefactor agents list                 # check access
prefactor setup <agent_id>            # print PREFACTOR_* values for an agent's SDK
prefactor setup --create --name "Support bot"   # or create the agent first
```

In a terminal, commands that are missing an ID let you pick it from a list: `prefactor setup` alone offers your agents, or a new one. Add `--json` to any command for JSON output.

## Skills for coding agents

The `skills/` directory holds skills that teach a coding agent to use the CLI:

- `bootstrap-existing-agent-with-prefactor-cli`: provision an agent's credentials with `prefactor setup` and write them into the project.
- `inspect-agent-run-with-prefactor-cli`: investigate an agent run from its spans and the agent's code.

Install them with the [skills](https://github.com/vercel-labs/skills) CLI:

```sh
npx skills add https://github.com/prefactordev/cli
```

The skills for instrumenting an agent with the SDK are in the [Prefactor TypeScript SDK](https://github.com/prefactordev/typescript-sdk/tree/main/skills).

## Commands

| Command | Description |
|---------|-------------|
| `prefactor login` | Sign in through the browser and save the token to the `default` profile |
| `prefactor profiles list` | List configured profiles |
| `prefactor profiles add <name> [baseUrl] --api-token <token>` | Add or update a profile |
| `prefactor profiles remove <name>` | Remove a profile |
| `prefactor setup [agent_id] [--create --name <name> [--description <text>]]` | Mint a deployment token for an agent and print `PREFACTOR_*` setup values, or JSON with `--json` |
| `prefactor ping [--api-token <token>] [--api-url <url>]` | Verify the selected or supplied API token |
| `prefactor pfid generate [--count <n>] [--account-id <id>]` | Generate Prefactor IDs |
| `prefactor version` | Print the CLI version |
| `prefactor install`, `update`, `doctor`, `uninstall` | Manage the installed CLI (see [Install](#install)) |

Resource commands cover every operation in the Prefactor API, grouped by resource; `api/coverage.yaml` lists the few left out on purpose. The groups are `accounts`, `environments`, `agents`, `judges`, `agent-versions`, `agent-schema-versions`, `agent-deployments`, `agent-associations`, `agent-instance-associations`, `agent-knowledgebase-links`, `agent-risk-versions`, `agent-instances`, `agent-spans`, `alerts`, `teams`, `people`, `risk-profiles`, `integrations`, `api-tokens`, `admin-users`, `admin-user-invites`, `llm-providers`, `llm-provider-models`, `knowledgebases`, `knowledgebase-documents`, `knowledgebase-document-revisions`, `knowledgebase-revisions`, `bulk` and `utils`. Run `prefactor <group> --help` for the subcommands.

Each command has an option for every field of its API request, named after the field in kebab case: `agent_version_id` is `--agent-version-id` and `time_range[start_at]` is `--time-range-start-at`. List commands take `--sort-field`, `--sort-direction`, `--pagination-offset` and `--pagination-page-size`. JSON options accept a JSON value or `@path/to/file.json`.

### Global flags

| Flag | Environment variable | Effect |
|------|----------------------|--------|
| `--json` | `PREFACTOR_OUTPUT_FORMAT=json` | Print JSON from every command except `--version`/`-V`, including `version`, `profiles` and `login` (whose instructions and token prompt then go to stderr). The variable only sets the default, so `--content-only` still prints plain content, and values other than `json` are ignored |
| `--fields <a,b.c>` | | Keep only these fields of the JSON output. Paths continue through arrays, so `summaries.id` keeps the id of every summary. Implies `--json` |
| `--no-input` | `PREFACTOR_NO_INPUT=1` | Never prompt |
| `--yes` | | Accept the default answer to every prompt |

The CLI is interactive only when stdin and stdout are terminals and no coding agent is detected: none of `CLAUDECODE`, `CURSOR_AGENT`, `GEMINI_CLI`, `CODEX_SANDBOX`, `CODEX_CI`, `AGENT` or `AI_AGENT` is set to a value other than empty, `0`, `false`, `no` or `off`. Otherwise a command that needs missing input fails at once and names the flag to pass, so an agent never waits on a prompt.

### `setup`

`setup <agent_id>` checks the agent exists, chooses an environment, creates a deployment token for the agent there, pings with it to check it, and prints the values an SDK reads: `PREFACTOR_API_URL`, `PREFACTOR_API_TOKEN`, `PREFACTOR_AGENT_ID` and `PREFACTOR_AGENT_IDENTIFIER`. `--create --name <name>` creates the agent first. The environment is that of the agent's only deployment, or of its only deployment with a current version; an agent with no deployment uses the first environment of the first account.

In interactive mode, `setup` with no agent shows a picker of agents with a choice to create one, and asks for the new agent's name and description. It asks which environment to use instead of choosing when the agent has several deployments, or, with no deployment, when there are several accounts or environments. Outside interactive mode it fails or chooses as above, like the TypeScript CLI.

### Output

At a terminal, results are drawn for reading:
- Lists are tables, with the name, status, a time and the ID first. They drop the least useful columns to fit the width, and say where the page sits in the list.
- Single resources are cards, with counts such as `instance_counts` drawn as bars.
- Spans, instances and alerts get a duration and a timeline of when each ran.
- Statuses are coloured, times are relative, and `prefactor --help` lists every command group.

Output that goes to a pipe or a file, `--json`, `--fields`, `PREFACTOR_OUTPUT_FORMAT=json` and a detected coding agent all get JSON and plain help, exactly as the TypeScript CLI prints them. `NO_COLOR` keeps the layout without colour.

### Pickers and `@latest`

In interactive mode, a command missing a required resource ID shows a list to choose from (type `/` to search), for example the agents for `prefactor agent-instances list`. Every other check runs first, so the CLI never asks you to pick for a command that would fail anyway. Resources that belong to another, such as an agent's instances, ask for the parent first unless the command already has it. A list with one entry is used without asking, except on commands that delete, revoke, terminate, archive, retire, suspend or discard data. Pickers draw on stderr, so stdout keeps the command's output; they show at most 1000 entries, the newest first except for revisions, which the API cannot sort. `agent-instance-associations` pickers ask for a judge, then one of its runs.

There are no pickers outside interactive mode, when stderr is not a terminal, with `--yes`, or with JSON output (`--json`, `--fields` or `PREFACTOR_OUTPUT_FORMAT=json`); a missing ID then fails with the argument or flag to pass.

`@latest` stands for the instance of an agent with the highest ID, which is the newest one created, wherever an agent instance ID is accepted, including inside ID lists: `prefactor agent-instances retrieve @latest`. Scope it to an agent with `<agent_id>/@latest`, which works without a terminal. Without a scope, the agent comes from the command's `--agent-id`, or from a picker in interactive mode. Where a judge run is expected, as in `agent-instance-associations list-from --agent-instance-ids`, `<judge_id>/@latest` is the judge's newest run. Case does not matter.

`--profile <name>` or `PREFACTOR_PROFILE` selects a profile. Without either, the `default` profile is used, and `PREFACTOR_API_TOKEN` and `PREFACTOR_API_URL` apply when it is not configured.

Profiles live in `prefactor.json`, looked up as the TypeScript CLI does: first in the root of the git repository that contains the current directory (or the current directory outside a repository), then next to the binary, or in the parent of its directory when that directory is named `bin`. Symlinks to the binary are followed. The managed install at `~/.prefactor/bin/prefactor` therefore uses `~/.prefactor/prefactor.json`.

### Differences from the TypeScript CLI

Commands, flags, output, help and errors match the TypeScript CLI, renamed to kebab case. Command-line mistakes are reported in Commander's words and order. These differences are deliberate:

- Requests send `sorting[field]`/`sorting[direction]` and `agent_ids[]`/`agent_instance_ids[]`/`statuses[]`, the forms p2 accepts. The TypeScript CLI sends `sorting=-name` and repeated `agent_ids=` or `statuses=`, which p2 rejects.
- Help for a subcommand ends with a "Global Options:" section listing `--profile` and the flags below it.
- When the server does not offer the browser login, `login` asks for a token only in interactive mode. It does not read one piped on stdin; use `prefactor profiles add default <baseUrl> --api-token <token>` instead.
- The `X-Prefactor-SDK` header is `prefactor-cli@<version>` rather than `@prefactor/core@<version>`.
- Errors that come from the runtime are worded by Go rather than Bun, for example invalid JSON in a flag, a missing `@file` and network failures.
- Pickers, `@latest`, `--json`, `--fields`, `--no-input` and `--yes` are new.
- Commands and options for API operations and request fields the TypeScript CLI lacks are new: `agent-instances create` and `send-message`, `admin-users update-role`, `admin-user-invites resend`, `api-tokens update`, the `utils`, `agent-knowledgebase-links` and `agent-risk-versions` groups, and options such as `environments create --purpose`. Their values are checked as the TypeScript CLI checks its own, with choices listed in help. Where p2 is stricter than the OpenAPI document, the CLI says so: `--pagination-page-with-id` is refused without `--sort-field`, and the help for `environments create --purpose` says p2 requires it. The option stays optional there so that the command still matches the TypeScript CLI, which has no `--purpose`.
- `playground` is left out.
- `install --version <version>` and `update --version <version>` install that version. The TypeScript CLI prints its own version for them instead, so a pinned install or update there does nothing.
- `install.ps1` adds the bin directory to the user's `PATH`.
- `setup` fails with "API token create response is missing token." when the token response has no token, where the TypeScript CLI prints `undefined` as the token and pings with it.
- IDs in a request path are escaped, as the OpenAPI document's single path segment requires: `teams retrieve a/b` requests `/api/v1/team/a%2Fb`. Many TypeScript commands put the ID in the path as given, so `a/b`, `?` and `#` change the request there.
- Integer options such as `--pagination-page-size`, `--pagination-offset` and `pfid generate --count` refuse values beyond the 64-bit integer range, which the API declares them as. The TypeScript CLI rounds them and sends values such as `1e+22`.
- JSON options are sent as written, so duplicate keys, number spelling such as `1.50`, and large integers reach the API unchanged; a number too large for JSON, such as `1e400`, is an error. The TypeScript CLI parses and re-serializes them, which keeps the last duplicate, writes `1.5`, rounds large integers and turns `1e400` into `null`.
- Base URLs must be well formed: `profiles add` and requests refuse an empty URL, surrounding spaces, `http:/host` and backslashes, which the TypeScript CLI's URL parser accepts. Both refuse a port above 65535.
- `uninstall` also removes the empty `~/.prefactor/bin` and `~/.prefactor` directories, which the TypeScript CLI means to remove but leaves behind.
- Request bodies list their fields in alphabetical order, and requests without a body still send `Content-Type: application/json`.
- `login` gives up on a request that takes more than 30 seconds, and `update` on a download that takes more than 5 minutes. The TypeScript CLI waits indefinitely.
- `update` and the install scripts download releases from `prefactordev/cli` rather than `prefactordev/typescript-sdk`.

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

`test/parity/contract_test.go` checks the requests this CLI sends against the document: `test/parity/contract-cases/` has a valid invocation of every operation, and each request's operation, parameters, body fields and types must match it. It runs with `go test ./...`.

`api/coverage.yaml` maps every operation in the OpenAPI document to the command that calls it, or marks it excluded or pending. `go test ./...` fails when an operation is missing from it, and when a command has no option for a field of its operation's request (`internal/cli/request_fields_test.go` lists the few exceptions).

### Parity with the TypeScript CLI

`test/parity` runs the cases in `test/parity/cases/` through the TypeScript CLI and this CLI against a recording server, and compares requests, output and exit codes. It also compares every command's `--help`, except the root's, which lists options and commands only one CLI has. Options and commands this CLI adds from the API are left out of the comparison:

```sh
PARITY_TS_CLI="bun ~/code/typescript-sdk/packages/cli/src/bin/cli.ts" go test -tags parity ./test/parity/
```

Build the TypeScript SDK first (`bun run build` in that repository), because the TypeScript CLI runs against the built `@prefactor/core`.

### Releases

Pushing a tag `vX.Y.Z` releases that version: `.github/workflows/release.yml` builds the archives with [GoReleaser](https://goreleaser.com) (`.goreleaser.yaml`) as a draft release with `SHA256SUMS` and GitHub's generated release notes, smoke tests them on macOS, Linux (glibc and musl) and Windows, on x64 and arm64, then publishes the release, which makes it the one the stable channel installs. Versions continue from the TypeScript CLI's last release, v0.6.1, so the first release here is v0.7.0 or later; the workflow refuses an older tag.

Each push to `main` replaces the `canary` pre-release, which the latest channel installs, after the same smoke tests (`.github/workflows/canary.yml`). Its version is the next patch version with the commit, such as `0.7.1-canary.1a2b3c4`.

After a release or canary is published, `.github/workflows/verify-install.yml` installs it from GitHub with the real install scripts on every platform.

The archive names are the TypeScript CLI's, which both CLIs' `update` download: `prefactor-<darwin|linux|windows>-<x64|arm64>.<tar.gz|zip>`, and `prefactor-linux-<x64|arm64>-musl.tar.gz`, which holds the same static Linux build. To build them locally and run the smoke test:

```sh
goreleaser release --snapshot --clean
scripts/smoke.sh dist canary
```

`go test ./...` runs `install.sh`, and `install.ps1` when `pwsh` is installed, against a local release server (`test/installers`).

### Command-line checks

`internal/cli/commander_flags.go` holds each option's string, choices and argument parser as the TypeScript CLI defines them. It is generated from `internal/cli/testdata/typescript-commands.json` by `go generate ./...`. To refresh the JSON after the TypeScript CLI changes:

```sh
bun tools/commandergen/dump-commands.ts ~/code/typescript-sdk/packages/cli/src/cli.ts > internal/cli/testdata/typescript-commands.json
```

`go test ./...` checks every command, argument and option against it, including descriptions and option order. A command or option the TypeScript CLI lacks must call an API operation or send one of its request fields.
