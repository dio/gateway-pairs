# goreleaser + Homebrew-tap Pattern (dio CLI repos)

Reference implementation: `github.com/dio/envoy-mini-builder` and `github.com/dio/gateway-pairs`

## .goreleaser.yml skeleton

```yaml
version: 2

project_name: <binary-name>

before:
  hooks:
    - go mod tidy
    # Run asset generation before Go compilation so embed directives resolve.
    # EG_VERSION (or similar version pin) is injected from the release workflow env.
    - make generate-assets EG_VERSION={{ .Env.EG_VERSION }}

builds:
  - id: <binary-name>
    main: ./cmd/<binary-name>
    binary: <binary-name>
    env:
      - CGO_ENABLED=0
    goos: [darwin, linux]
    goarch: [arm64, amd64]
    ldflags:
      - -s -w
      - -X main.version={{.Version}}
      - -X main.egVersion={{.Env.EG_VERSION}}   # domain-specific version pin
      - -X main.commit={{.Commit}}
      - -X main.date={{.Date}}

archives:
  - id: default
    format: tar.gz
    name_template: "{{ .ProjectName }}_{{ .Version }}_{{ .Os }}_{{ .Arch }}"
    files:
      - README.md
      - LICENSE
      - docs/

checksum:
  name_template: "checksums.txt"

release:
  github:
    owner: dio
    name: <repo-name>
  draft: false
  prerelease: auto
  name_template: "v{{.Version}}"

brews:
  - name: <binary-name>
    directory: Formula
    repository:
      owner: dio
      name: homebrew-tap
      branch: main
      token: "{{ .Env.HOMEBREW_TAP_GITHUB_TOKEN }}"
    commit_author:
      name: Dhi Aurrahman
      email: dio@rockybars.com
    homepage: "https://github.com/dio/<repo-name>"
    description: "<one-line description>"
    license: "MIT"
    install: |
      bin.install "<binary-name>"
    test: |
      system "#{bin}/<binary-name>", "version"
```

## Key rules

- **`EG_VERSION` (or equivalent pin) belongs in the release workflow `env` block**, not in `.goreleaser.yml`. Single bump point.
- **goreleaser v2 has no native Helm chart publishing.** Use a separate `publish-charts` job after the goreleaser job.
- **`before.hooks` must generate all embedded assets** before Go compilation. `//go:embed` resolves at compile time -- if the directories are empty, the build fails.
- **`{{ .Version }}` strips the leading `v`** (0.1.0 not v0.1.0). Use `{{ .RawVersion }}` if you need the v-prefix. Helm chart versions must not have v-prefix.

## Release workflow (.github/workflows/release.yml)

```yaml
name: Release

on:
  push:
    tags: ["v*"]

permissions:
  contents: write
  packages: write  # for ghcr.io chart publishing

jobs:
  goreleaser:
    runs-on: ubuntu-latest
    env:
      EG_VERSION: v1.8.0  # bump here when upgrading the pinned dep
    steps:
    - uses: actions/checkout@de0fac2e4500dabe0009e67214ff5f5447ce83dd # v6.0.2
      with:
        fetch-depth: 0  # goreleaser needs full history for changelog
    - uses: actions/setup-go@4a3601121dd01d1626a1e23e37211e3254c1c06c # v6.4.0
      with:
        go-version-file: go.mod
        cache: true
    - uses: azure/setup-helm@dda3372f752e03dde6b3237bc9431cdc2f7a02a2 # v5.0.0
    - uses: goreleaser/goreleaser-action@5daf1e915a5f0af01ddbcd89a43b8061ff4f1a89 # v7.2.2
      with:
        version: "~> v2"
        args: release --clean
      env:
        GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
        HOMEBREW_TAP_GITHUB_TOKEN: ${{ secrets.HOMEBREW_TAP_GITHUB_TOKEN }}
        EG_VERSION: ${{ env.EG_VERSION }}

  publish-charts:
    runs-on: ubuntu-latest
    needs: goreleaser
    env:
      EG_VERSION: v1.8.0
      CHART_VERSION: ${{ github.ref_name }}
    steps:
    - uses: actions/checkout@de0fac2e4500dabe0009e67214ff5f5447ce83dd # v6.0.2
    - uses: azure/setup-helm@dda3372f752e03dde6b3237bc9431cdc2f7a02a2 # v5.0.0
    - name: Login to ghcr.io
      run: echo "${{ secrets.GITHUB_TOKEN }}" | helm registry login ghcr.io -u ${{ github.actor }} --password-stdin
    - name: Package and push charts
      run: |
        VERSION="${CHART_VERSION#v}"  # strip leading v
        for chart in eg-crds eg-pair; do
          helm package "charts/${chart}" --version "${VERSION}" --app-version "${EG_VERSION}"
          helm push "${chart}-${VERSION}.tgz" "oci://ghcr.io/${{ github.repository_owner }}/<repo-name>/charts"
        done
```

## Pinned Action SHAs (verified 2025-05)

```
actions/checkout@de0fac2e4500dabe0009e67214ff5f5447ce83dd          # v6.0.2
actions/setup-go@4a3601121dd01d1626a1e23e37211e3254c1c06c           # v6.4.0
azure/setup-helm@dda3372f752e03dde6b3237bc9431cdc2f7a02a2            # v5.0.0
goreleaser/goreleaser-action@5daf1e915a5f0af01ddbcd89a43b8061ff4f1a89 # v7.2.2
```

Refresh with:
```bash
for action in actions/checkout actions/setup-go azure/setup-helm goreleaser/goreleaser-action; do
  tag=$(gh api repos/$action/releases/latest --jq '.tag_name')
  obj=$(gh api repos/$action/git/ref/tags/$tag --jq '.object')
  sha=$(echo "$obj" | jq -r '.sha')
  type=$(echo "$obj" | jq -r '.type')
  if [ "$type" = "tag" ]; then
    sha=$(gh api repos/$action/git/tags/$sha --jq '.object.sha')
  fi
  echo "$action@$sha # $tag"
done
```

## Chart version sentinel

Set `version: 0.0.0-dev` in `Chart.yaml`. Never bump manually.
`helm package --version $VERSION` injects the correct version at release time.
`appVersion` also injected with `--app-version $EG_VERSION`.

## Embedded assets and go:embed

Put `embed.go` INSIDE the directory being embedded:

```
charts/
  embed.go          # package charts; //go:embed eg-crds eg-pair all:crds
  eg-crds/
  eg-pair/
  crds/
    .gitkeep        # committed -- makes all:crds compile on clean checkout
    *.yaml          # gitignored -- generated by make generate-crds
```

Use `all:crds` not `crds` -- `all:` includes hidden files (`.gitkeep`).
Without `all:`, the directive fails if only `.gitkeep` is present (no non-hidden files).

`generate-crds` Makefile target writes YAML to `charts/crds/`. Must run before
`go build`. goreleaser `before.hooks` calls it:
```yaml
- make generate-assets EG_VERSION={{ .Env.EG_VERSION }}
```
