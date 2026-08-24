# Contributing to ECG Hub

ECG Hub runs in hospitals, on real patient recordings. That shapes what a good
contribution looks like here: small, verified against a running stack, and
honest about what was not tested.

## Before you start

- **Bug or question:** open an issue. Include the version or commit, what you did, what happened, and the relevant log lines.
- **Security problem:** do not open an issue — see [SECURITY.md](SECURITY.md).
- **New feature or a vendor module:** open an issue first. Ingestion modules touch clinical data paths and are easier to design together than to review after the fact.
- **Never attach real patient data.** Not in issues, not in tests, not in screenshots. Anonymise, or synthesise a file — `backend/internal/connector/dicom/testdata/` shows the expected shape.

## Getting a stack running

```bash
make init                                     # .env and config.yaml from the examples
docker compose -f docker-compose.dev.yml up -d # Postgres is expected on the host
```

The UI is on http://localhost, the API under `/api/v1`, and the first visit
walks you through creating the administrator account. `docs/deploy-prod.md`
covers the production layout.

## Working on the code

```bash
cd backend  && go build ./... && go vet ./... && go test ./...
cd frontend && yarn typecheck && yarn build
```

CI runs exactly those (`.github/workflows/ci.yml`). Both must pass — the test
suite is green today and we intend to keep it that way.

`yarn lint` currently reports pre-existing errors; it is not in CI yet. Do not
add new ones.

Notes that save review time:

- **Protobuf.** The gRPC/Connect API is generated: edit `backend/v1/*.proto`, then `cd backend && buf generate`, and commit the generated Go and TypeScript.
- **Frontend text is translated.** Every user-facing string goes through `t()`, with the key added to both `en.json` and `fr.json`.
- **Errors shown to users** go through `errorMessage()` (`frontend/src/lib/errors.ts`) so transport codes do not leak into toasts.
- **Comments explain why, not what.** The codebase leans on this; a comment that restates the line below it will be asked about.

## Testing expectations

- Logic with a branch, a parser or a security decision comes with a test.
- Anything touching ingestion, HL7 or webhooks should also be exercised against a running stack — `tools/webhook-api-test.py` does that end to end for the webhook REST API, and is the pattern to follow.
- Say in the pull request what you verified and what you did not. "Not tested against a real PACS" is useful; silence is not.

## Pull requests

- One concern per pull request. A drive-by fix in another area is a second PR.
- Explain the bug, not only the patch: what went wrong, why the code allowed it, what the fix changes.
- Reference the issue (`Closes #123`).
- Commit messages: imperative mood, a body that would still make sense to someone reading `git log` in two years.

Reviews focus on correctness and clinical safety first, then on whether the
change is the smallest one that solves the problem.

## Licence

By contributing you agree that your work is licensed under the
[Apache 2.0 licence](LICENSE) that covers this repository.
