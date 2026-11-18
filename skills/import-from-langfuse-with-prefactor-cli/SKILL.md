---
name: import-from-langfuse-with-prefactor-cli
description: Use when the user wants to bring agents and past runs that are traced in Langfuse into Prefactor, or mentions Langfuse together with Prefactor. Runs `prefactor import langfuse` in the agent's repository with the Langfuse keys already on the machine.
---

# Import from Langfuse with Prefactor CLI

Import an agent's Langfuse traces into Prefactor with one CLI command. The CLI reads Langfuse with the keys on this machine and sends only the runs to Prefactor. Prefactor never receives the Langfuse keys.

## When to use

Apply this skill when the user asks to:

- "import my Langfuse traces into Prefactor"
- "bring my agent's history from Langfuse"
- "move from Langfuse to Prefactor"
- "I already trace my agent with Langfuse"

## Prerequisites

- The Prefactor CLI is installed: `prefactor version` prints a version. If it is missing, ask the user to install it with `curl -fsSL https://raw.githubusercontent.com/prefactordev/cli/main/install.sh | bash` (macOS and Linux) or `irm https://raw.githubusercontent.com/prefactordev/cli/main/install.ps1 | iex` (Windows PowerShell).
- You are in the agent's repository, where its Langfuse keys are usually set.
- The Langfuse project is not in Langfuse's HIPAA region. Prefactor does not accept data from it.

## Langfuse keys

The CLI reads `LANGFUSE_PUBLIC_KEY`, `LANGFUSE_SECRET_KEY` and `LANGFUSE_BASE_URL` (or `LANGFUSE_HOST`) from the shell environment, then the nearest `.env` up to the repository root, then the `[env]` section of `mise.local.toml` and `mise.toml`. `--env <file>` reads only that file.

Never ask the user to paste Langfuse keys into the chat, and never print, echo or read out their values. Keys in the chat go to the model provider. When the keys are missing, or cannot read traces, ask the user to set them themselves:

1. In Langfuse, open the project, go to Project Settings > API Keys, choose "Create new API keys" and pick the Viewer role. Full access and Developer keys also work.
2. Put the keys in the shell environment, or in a `.env` file that is not committed (check `.gitignore`), for example `.env.langfuse`, and pass `--env .env.langfuse`.

Do not commit `.env` files or `prefactor.json`.

## Signing in to Prefactor

If the CLI is not signed in, `prefactor import langfuse` opens the browser for the user to approve the sign-in, then carries on. Tell the user to approve it. Do not run `prefactor profiles add` or invent API tokens. If the sign-in cannot start, ask the user to run `prefactor login` in their own terminal.

## Run the import

Look at what would be imported first. This reads Langfuse only and sends nothing to Prefactor:

```bash
prefactor import langfuse --dry-run
```

Then import. `--yes` takes the defaults: every agent found, over the last 30 days. Without `--yes`, a coding agent's shell cannot answer the prompts, so the command stops.

```bash
prefactor import langfuse --yes
```

Useful options:

- `--days <n>` looks back n days instead of 30.
- `--env <file>` reads the Langfuse keys only from that file.
- `--account-id <id>` chooses the Prefactor account when the user can reach several.

The command reads Langfuse 50 observations at a time and waits whenever Langfuse's rate limit asks it to (30 requests a minute on the Hobby plan), so a large import can take many minutes; the dry run gives an estimate. Let it run. If it stops, run the same command again: runs already imported are skipped.

Each Langfuse trace name becomes a Prefactor agent, and each Langfuse environment becomes a deployment. To choose, rename or merge agents, the user can run `prefactor import langfuse` in their own terminal.

## Report back

The command prints JSON with:

- `runs`: how many were sent, imported (`written`), skipped because they were already imported, and rejected.
- `rejected`: each rejected trace ID with its reason.
- `agents_url`: the page that lists the imported agents.
- `next_step`: run `prefactor setup` to send new runs through the Prefactor SDK.

Tell the user the counts, any rejection reasons, and the agents page link. Then suggest instrumenting the agent with the Prefactor SDK for new runs, starting with the `bootstrap-existing-agent-with-prefactor-cli` skill. The import does not sync new Langfuse runs.

## Common mistakes

- Asking the user to paste Langfuse keys into the chat.
- Printing a `.env` file to check the keys.
- Running the import without `--dry-run` first on a large project.
- Stopping a long import early because it looks slow.
