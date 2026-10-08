# Changelog

All notable changes to this project are documented in this file.
The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [0.9.7] - 2026-10-09

### Added

- `tools/e2e/run-e2e.sh`: end-to-end test with no real panel. It stands up a stub
  Xboard panel, pushes real traffic through the node with a real xray-core client
  (`curl -> socks -> VMess/WS -> node -> freedom -> local HTTP server`), and checks
  that the node moves its listener when the panel changes the port and rolls back
  when a config cannot be applied. Entirely local, so it needs no secrets and no
  network. Now runs in CI.
- `release/install.sh`: one-click installer for Debian. Detects the architecture and
  Debian release, installs the required packages, builds from source (installing Go
  when missing) or unpacks a prebuilt release, creates `/etc/XrayR`, downloads the
  rule data, installs the systemd unit, validates the configuration and starts the
  service. Supports `--init`, `--release`, `--source-dir`, `--skip-rules`,
  `--no-start` and `--uninstall [--purge]`, and is idempotent on re-run.
- `release/download-rules-dat.sh`: downloads the latest `geoip.dat` / `geosite.dat`
  from [Loyalsoldier/v2ray-rules-dat](https://github.com/Loyalsoldier/v2ray-rules-dat)
  and verifies the published sha256 checksum.
- `release/systemd/XrayR.service`: systemd unit with `ExecStartPre` configuration
  validation, matching what `docs/installation.md` documents.
- `api.AppendRule`: compiles a panel-supplied audit rule pattern and skips it with a
  warning instead of panicking when the panel sends an invalid regular expression.
- `api.NewHTTPClient`, `api.ReadLocalRuleList`, `api.CheckResponse`: shared adapter
  helpers that replace seven near-identical copies.
- Offline unit tests using `httptest`: `api/client_test.go`,
  `api/sspanel/offline_test.go`, `internal/snapshot/snapshot_test.go`,
  `internal/redact/redact_test.go`, `panel/ruledata_test.go`,
  `service/controller/state_race_test.go`.
- CI now runs `go test -race ./...`, `golangci-lint run`, `shellcheck` on
  `release/*.sh`, and validates `release/config/route.json` against freshly
  downloaded rule data.
- `.golangci.yml`: conservative linter set (`govet`, `ineffassign`, `staticcheck`,
  `unused`, `gofmt`). `errcheck` is disabled with an explanation; enabling it is a
  follow-up.

### Changed

- `Controller` runtime state (`nodeInfo`, `userList`, `Tag`) is published through an
  `atomic.Pointer[runtimeState]` snapshot instead of unsynchronized fields. The node
  monitor and the user monitor run on the same interval and previously raced on the
  user list.
- Periodic tasks are wrapped in `Controller.guardTask`, which recovers from panics.
  xray-core's `task.Periodic` neither recovers nor survives a non-nil error, so an
  unguarded panic used to terminate the whole process.
- Not-modified detection uses sentinel errors (`api.ErrNodeNotModified`,
  `api.ErrUserNotModified`, `api.ErrRuleNotModified`) compared with `errors.Is`,
  replacing `err.Error() == "..."` string comparisons.
- `parseConnectionConfig` returns an error instead of calling `log.Panicf`.
- `mydispatcher.Dispatch` returns an error for an invalid destination instead of
  panicking.
- `v2raysocks.GetNodeRule` returns the local rule list when the node configuration
  has not been fetched yet, instead of dereferencing a nil `ConfigResp`.
- Configuration reload records the attempt even when the new configuration is
  rejected, so a burst of invalid writes no longer re-runs validation on every event.
- Configuration reload exits with a non-zero status when neither the new nor the
  previous configuration can start, instead of staying alive while serving nothing.
- `defer p.Close()` on shutdown closes the panel that is actually running.

### Removed

- `release/config/geoip.dat` and `release/config/geosite.dat` are no longer committed.
  They are downloaded on every install; see `docs/installation.md`.
- Dead `assembleURL` helpers in six panel adapters.
- `api.NodeNotModified` / `api.UserNotModified` / `api.RuleNotModified` string
  constants, superseded by the sentinel errors above.
- Dead code reported by `golangci-lint`: `getConfig` in `cmd/root.go`,
  `LegoCMD.getPath` / `LegoCMD.getCertConfig`, `integerDescription` in
  `internal/configui`, and the unused `Writer.w` field in `common/limiter`.

### Fixed

- `release/config/route.json` contained a rule with an empty `domain` list, which
  xray-core rejects with `this rule has no effective fields` when the instance is
  created. Users who enabled `RouteConfigPath` could not start the node at all.
- `ReadLocalRuleList` dereferenced a nil `*os.File` when the rule file could not be
  opened, and called `log.Fatalf` on a read error, terminating the process.
- `os.Kill` was passed to `signal.Notify`, which can never deliver it.
- A periodic task that stopped because `Execute` returned an error did so silently;
  the reason is now logged.
