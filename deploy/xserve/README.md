# xserve deployment and upstream maintenance

Fork: https://github.com/turbosasquatch/upbrr

Upstream: https://github.com/autobrr/upbrr

Production uses the reviewed `xserve` branch, initially based on official v0.4.1 (`9bb65cfe2c924bd6d5363fab3d39177b6017b689`), with small reliability patches. Unreleased upstream main changes are not part of that base. Keep each local patch and its regression tests identifiable so it can be removed when upstream includes an equivalent fix.

upbrr and Whackamole run on xserve. Media and the torrent client remain on Unraid. The restricted NFS mount is read/write, restricted to xserve and mapped to the media owner. Source and staging paths must stay on the same Unraid filesystem so staging creates genuine hardlinks. Copy fallback stays disabled. The old Upload Assistant configuration, encrypted keys and worker data remain available for rollback.

## Current patch scope

- Prefer an already complete, strictly verified exact-source client candidate within the existing piece-size preference class. Preserve all source and content verification requirements.
- Reuse successful local byte validation within the same live workflow, source generation, complete manifest, coordinator/fence and runtime configuration. File device/inode/size/mtime/ctime, torrent layout or authority changes invalidate reuse. Failed/cancelled validations and forced rechecks never authorize reuse. Hardlink creation can change ctime, causing a conservative cache miss. Remote authentication, duplicates, policy and approval are always reviewed independently.
- Request complete programme-track collection for YUS packs while retaining strict identity and language requirements. This does not guarantee that a pack satisfies YUS rules.

## Stable upstream review

The weekly `Fork upstream review` workflow compares `xserve` with the latest official stable release and opens one review issue for each new release. It never merges or deploys. Run it manually for an immediate check. Enable repository Issues and Actions, and set the reviewed production branch as the repository default so scheduled reviews execute there.

1. Branch from `xserve`, merge the stable release tag and resolve conflicts without dropping local fixes.
2. Review release notes, CLI flags, tracker behavior, schema migrations and local-patch overlap. Update both VERSION and OCI version in xserve-image.yml for the new base/patch release, and review Whackamole’s corresponding exact-version allowlist. Remove a local patch only after equivalent upstream behavior and tests are confirmed.
3. Run focused race tests, `make test-go`, `make lint`, `make logpolicy`, `make pathpolicy`, `make backend`, `make gofix-check-changed` and whitespace checks. Shared workflow/runtime changes require all five isolated E2E projects. These tests use synthetic data and local fake services.
4. Open a PR to `xserve`, document validation and review the full patch. Move the production branch only after review. Record the exact SHA; do not force-reset the fork to upstream.
5. Manually run `checks` and `e2e` on that exact production SHA. The `xserve image` workflow refuses publication without successful manual runs of both for the same commit. It publishes both architectures under a commit tag, with the full build SHA embedded. Publication does not deploy. Review and record its immutable image index digest and the selected ARM64 child digest/binary checksum.

## Deployment and validation

1. Pause admissions and Auto Upload. Let any active operation finish, or investigate and recover an interrupted operation using the exact reviewed native recovery procedure. Do not treat a dead process as proof that an upload was not accepted. Preserve failed/accepted receipts, operation records and submission fences; never retry an accepted or uncertain submission.
2. With all database users quiescent, take private complete matching backups: native SQLite, config, `web-auth.json`, queues and retained artifacts; Whackamole SQLite, config, `secret.key`, `secrets.yaml` and retained outcomes. Check both database integrities. Label any known orphan explicitly; an orphan snapshot is not a clean-idle backup. Do not print keys or commit backups.
3. Update both native service and Whackamole's embedded CLI to the same reviewed immutable image. Whackamole must explicitly recognize that exact image/version/storage contract. Unknown images fail closed. Verify the copied binary checksum matches the standalone service, migration set and observed state shape before admission.
4. Use a separate isolated profile for tests. Exercise NORMAL readiness/review with an execution boundary that cannot submit to trackers or inject clients. Verify the boundary and inspect each selected lane. `--debug --no-seed --skip-imagehost-upload` is useful supplementary flow testing, but debug bypasses some policy gates and does not establish NORMAL upload eligibility. It can still create local artifacts.
5. Verify source identity, full manifests, policy/dupes and hardlink support. Preserve manual holds where required facts are absent. Resume normal admissions only after all tests and contract checks pass. Observe new outcomes; on failure pause and inspect retained state before taking action.
6. For every accepted lane, verify the native receipt, tracker ID, exact torrent hash, complete client delivery and true same-filesystem hardlinks. Record proofs without exposing credentials. Retain previous images and matching state snapshots. Take a fresh paired backup after a proven idle interval.

## Rollback

Pause admissions and quiesce all native database users. If storage remains compatible, prefer a reviewed image-only rollback while preserving current databases and newly accepted receipts. Native migrations are forward-only; never blindly start an older binary on a newer schema.

A full state restore requires the matching complete native and Whackamole backups plus an explicit reconciliation of every upload accepted since that snapshot. Restoring an old database alone can erase acceptance history and cause duplicate uploads. Preserve the current state before restoration and resolve uncertain effects first. Keep legacy Upload Assistant auth/data mounts and encrypted tokens intact.

Do not commit config exports, private URLs, media, API keys, tokens, auth state or backup archives.
