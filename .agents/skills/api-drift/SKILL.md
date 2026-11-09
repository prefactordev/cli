---
name: api-drift
description: Compares the OpenAPI document the live Prefactor API serves against this CLI's copy in api/openapi.json and the commands built on it, and emits the required changes as a staged roadmap of commit-sized agent prompts. Use when the Prefactor API or its OpenAPI document changed, when the spec-sync pull request needs a plan, when checking for API drift, or when the user asks what the CLI needs to match the backend.
disable-model-invocation: true
---

# API drift

The contract is the OpenAPI document the live API serves at `https://app.prefactorai.com/api/v1/openapi`. Never use a local server or another environment. `api/openapi.json` stores it with its keys sorted (`jq -S .`); compare and copy it in that form.

This analysis is read-only for the repository: work on copies under a temporary directory and a throwaway git worktree, and leave the working tree as it was.

## 1. Fetch the spec

```bash
T="$(mktemp -d)"
date -u +%Y-%m-%dT%H:%MZ
curl -fsSL https://app.prefactorai.com/api/v1/openapi | jq -S . > "$T/live-openapi.json"
jq -e '.paths | length' "$T/live-openapi.json"
```

Record the fetch time for the roadmap. If a command fails (network error, an error status, not JSON), stop and tell the user.

If `cmp -s api/openapi.json "$T/live-openapi.json"` succeeds there is no drift: say so with the fetch time and stop.

## 2. List every change

Two diffs, both from the repo root:

```bash
go run ./tools/specdigest api/openapi.json > "$T/old.txt"
go run ./tools/specdigest "$T/live-openapi.json" > "$T/new.txt"
diff -u "$T/old.txt" "$T/new.txt"
diff api/openapi.json "$T/live-openapi.json"
```

The digest diff shows structural changes: operations, parameters, bodies, responses, schemas, required and nullable fields, enums. The `jq` diff also shows what the digest leaves out, chiefly descriptions. Copy every hunk of both into working notes; each one must end up classified (step 5). Completeness is the point of this skill: a run that drops a change is a failed run.

## 3. Let the tests find the mechanical drift

The repository's tests already check the document against the CLI. Run them against the new document in a throwaway worktree:

```bash
git worktree add --detach "$T/wt" HEAD
cp "$T/live-openapi.json" "$T/wt/api/openapi.json"
(cd "$T/wt" && go generate ./... && go build ./... && go vet ./... && go test ./... 2>&1 | tee "$T/test.txt")
git worktree remove --force "$T/wt"
```

What each failure means is in [reference.md](reference.md#what-the-tests-check). Map every failure to the diff hunk that caused it.

## 4. Check what the tests cannot

For every hunk not explained by a test failure, check the CLI by hand. Mainly:

- **Response fields the CLI reads.** The tests check requests, not what the CLI reads back. Search for the field's name in `internal/` (for example `rg -n '"field_name"' internal/`); see [reference.md](reference.md#responses-the-cli-reads) for where responses are read.
- **Descriptions.** A changed operation or field description can make a command's or option's help text wrong. Compare it with the command's `Short` and the option's usage.
- **Behaviour the description states**, such as a default, a limit, or a side effect, that the CLI relies on or repeats to the user.

## 5. Classify

- **Breaking drift**: the CLI sends or reads something the document removed or changed incompatibly: a removed operation or field, a renamed field, a narrowed type or enum, or a new required request field.
- **Contract mismatch**: a real but compatible difference on an operation the CLI calls: a widened type, a new optional field the CLI reads nothing from, an optional/required flip in the compatible direction.
- **Coverage gap**: a new operation with no command, or a new request field or enum value with no option or choice.
- **Help text**: a description change that makes the CLI's help wrong or incomplete.
- **No change needed**: a hunk that changes nothing the CLI sends, reads or says (for example a description of a response field the CLI never shows). Say why in working notes.
- **Documented omission**: an operation `api/coverage.yaml` marks `excluded`, with its reason. Count it in the spec state; give it no stage.

Never decide a gap is intentional yourself. Only an `excluded` entry in `api/coverage.yaml` or a sentence in the README marks one; quote it.

Every finding cites both sides: the hunk from step 2 and the code (file and line) or test failure.

## 6. Stages

- One stage is one commit that leaves `go build ./...`, `go test ./...` and `golangci-lint run ./...` passing.
- Stage 1 always updates `api/openapi.json` to the fetched document, runs `go generate ./...`, fixes breaking drift, and keeps the tests passing for the gaps later stages close, following [reference.md](reference.md#keeping-stage-1-green).
- Then one stage per resource or coherent unit for coverage gaps, then help text. Each names the files to change, the spec shapes inline, and the tests to add or update (see the recipes in reference.md).
- Write each stage as a ready-to-paste prompt with its evidence inline. For any claim about the code you could not verify, give the executor the check and tell it to stop and report if the claim is false.

## 7. Roadmap

Present the roadmap as a plan (use plan mode if available), with one todo per stage:

1. **Header note**: "Roadmap, not a task plan. Run each stage below as its own prompt in a fresh session that plans it, or run them all in one session with the prompt below."
2. **Run all stages**:

   ````markdown
   ## Run all stages

   ```
   Work through every stage in this roadmap, in order. For each: do the work in its prompt, make `go generate ./...`, `go build ./...`, `go test ./...` and `golangci-lint run ./...` pass, then stop and wait for my feedback or an instruction to commit. Commit only when I say, using the stage's commit subject, then go on to the next stage.
   ```
   ````

3. **Spec state**: fetch time, and counts by class (breaking, mismatch, coverage gap, help text, no change needed, documented omission). Documented omissions appear only as a count.
4. **Coverage gaps**: every gap by name, each with the steps to close it. Never a count or a sample.
5. **Stages**, each in this form:

   ````markdown
   ### 1. <stage title>

   ```
   <imperative goal, with the spec evidence inline>. Work in <files>.

   Do not create a commit.
   ```

   - **Touches**: `api/openapi.json`, `internal/cli/...`
   - **Commit**: <commit subject>
   ````

## Non-goals

- Not an implementation pass: each stage gets its own plan later.
- Does not edit the working tree, create commits, or call any Prefactor API.
