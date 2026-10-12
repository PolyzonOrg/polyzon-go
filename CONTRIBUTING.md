# Contributing

Thanks for helping to improve the Polyzon Go SDK. Bug reports and pull requests
are welcome; for larger changes please open an issue first to agree on the
design. Security issues go to security@polyzon.org, see [SECURITY.md](SECURITY.md).

## Development

Requirements: Go 1.23 or newer. The module has no dependencies outside the
standard library; keep it that way.

```sh
gofmt -l .                       # must print nothing
go vet ./...
go run honnef.co/go/tools/cmd/staticcheck@v0.8.1 ./...   # needs Go <= 1.27.1 until the next staticcheck release
go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...
go test -race -cover ./...       # coverage must stay at or above 90 %
go build ./...                   # also builds examples/
```

Tests never touch the network or the real API: they run against `httptest`
servers, and retry waits are replaced by a recording hook. Do not add tests that
call api.polyzon.org.

## Pull requests

- One topic per pull request, with tests for every behaviour change.
- Use [Conventional Commits](https://www.conventionalcommits.org/) for titles,
  e.g. `fix: honour Retry-After HTTP dates`, `feat: add WithFoo option`.
- Document every exported identifier and update `README.md`, `README.ru.md`
  and `CHANGELOG.md` (section `Unreleased`).
- Never commit tokens, `.env` files or captured traffic.

## Releasing

Go modules are released by tags; there is no registry upload. For a release:

1. Move the `Unreleased` entries of `CHANGELOG.md` under the new version with
   today's date and update `Version` in `polyzon.go`.
2. Merge that change to `main` and wait for CI to pass.
3. Tag the merge commit and push the tag:

   ```sh
   git tag -a v1.0.0 -m "v1.0.0"
   git push origin v1.0.0
   ```

4. Create the GitHub release from the tag with the changelog section as notes.
   pkg.go.dev picks the version up from the module proxy within minutes
   (`GOPROXY=https://proxy.golang.org GO111MODULE=on go list -m github.com/PolyzonOrg/polyzon-go@v1.0.0`
   triggers it at once).

Versions follow [SemVer](https://semver.org/): a breaking change of the public
API needs a new major version with the `/v2` module path suffix.
