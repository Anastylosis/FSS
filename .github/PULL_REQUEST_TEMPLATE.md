## What

<!-- One-liner: what does this PR do? -->

## Why

<!-- What problem does it solve? Link to issue if applicable. -->

## How

<!-- Brief description of the approach. Call out anything non-obvious. -->

## Checklist

- [ ] `go build ./...` passes
- [ ] `go test -race -count=1 ./...` passes
- [ ] `go vet ./...` clean
- [ ] New scraper? Added blank import in `internal/scrapers/all/all.go`
- [ ] New config fields? Added to `internal/config/config.go` with defaults
- [ ] Tested manually against a live site (if applicable)
