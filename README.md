# Agent OS

## CI

The project uses a **self-hosted GitHub Actions runner** (not GitHub-hosted)
to avoid consuming the 2,000 minute/month cap on the Free plan.

### Registering the self-hosted runner

The workflow (`.github/workflows/ci.yml`) is **inert** until a runner is online.
To bring it up:

1. Go to **Settings → Actions → Runners → New self-hosted runner** in the repo.
2. Choose **Linux** and **x64**.
3. On the runner host, install the prerequisites:
   - **Go 1.26+**
   - **sqlc v1.31.1** (`go install github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1`)
   - **podman 5+** (or Docker — adjust the `podman` commands in the workflow)
   - **psql** client (for migration steps)
4. Run the provided `config.sh` and `run.sh` scripts from GitHub's setup page.
5. The runner will auto-pick up workflows; verify with a test push.

### What the workflow does

On every **pull request** and **push to `main`**:

1. **Builds** (`go build ./...`) and **vets** (`go vet ./...`).
2. **Generates** sqlc code (never committed — CI generates it).
3. Starts a **throwaway Postgres 17** container via podman on port 55434.
4. Runs all **up-migrations** in numeric order.
5. Runs the full test suite with `AOS_TEST_DSN` set so **integration tests
   actually run** — a skipped integration suite is treated as a failure.
6. Cleans up the Postgres container (even on failure).

Until the runner is registered, the local 3-gate loop and post-merge green
baseline (ADR-005 D6) continue to provide regression defense.
