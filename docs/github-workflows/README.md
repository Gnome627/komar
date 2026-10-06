# GitHub Actions

CI (`ci.yml`: gofmt, vet, tests with -race) and releases (`release.yml`: on a
`v*` tag, builds linux/amd64 and linux/arm64 tarballs that `install.sh`
downloads instead of building from source).

They live here because the automation that created the repo could not push
workflow files. To enable them:

```bash
mkdir -p .github/workflows && git mv docs/github-workflows/*.yml .github/workflows/
```
