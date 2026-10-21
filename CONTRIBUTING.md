# Contributing

Keep changes focused on the existing observation, consolidation, recall, and
storage boundaries. Include a synthetic reproduction when reporting a defect.
Never attach a personal chat export, production database dump, or credential.

## Checks

Use Go 1.26.10 or newer with a current patch release. Run:

```sh
go test -race -count=1 ./...
go vet ./...
go build ./...
go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...
go run github.com/zricethezav/gitleaks/v8@v8.30.1 git --redact --log-opts=--all
```

Database regression tests require an explicit `CORTEX_TEST_DATABASE_URL` pointing
at a disposable PostgreSQL database. They create and remove isolated schemas.
The database role needs schema and `pg_trgm` extension permissions. Never use a
production database for these checks.

The tests use fake inference and do not need a provider key. Changes to model
behavior should describe the model and inputs used, and keep generated text
separate from measured software guarantees.
