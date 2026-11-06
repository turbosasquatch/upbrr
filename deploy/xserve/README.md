# xserve deployment and upstream maintenance

Fork: https://github.com/turbosasquatch/upbrr
Upstream: https://github.com/autobrr/upbrr

Run the fork on xserve (Apple silicon), beside Whackamole. Mount the media share read-only. upbrr performs hashing and FFmpeg locally on xserve; the old Upload Assistant worker is retained solely for rollback.

## Update process

The weekly `Fork upstream review` workflow fetches upstream into a review branch and opens/updates a pull request. It never merges or deploys. Run it manually from Actions for an immediate check. Enable Actions on the fork if GitHub has disabled scheduled workflows.

1. Review the upstream PR, release notes, CLI flag changes, migration changes and tracker support.
2. Resolve conflicts on the review branch; run upstream backend/config/CLI tests and Whackamole integration tests.
3. Merge only after review. Record the exact commit SHA.
4. Stop upbrr and Whackamole. Back up the complete upbrr volume including SQLite, `web-auth.json` (encryption key), queues and retained artifacts, plus Whackamole's database/config/secret.key/secrets.yaml. Keep stopped-state backups private. Database migrations are forward-only.
5. Build the fork's Dockerfile for `linux/arm64` on xserve with version/build identifier set to the SHA. Pin the resulting image ID/digest in compose. Rebuild Whackamole with that exact upbrr binary.
6. Run `--debug --no-seed` on a separate test profile. For tests without image hosting also pass `--skip-imagehost-upload`. Inspect all tracker results; verify no tracker submissions or client additions.
7. Restart production only after tests pass. Retain the prior images and matching complete state backups.

## Rollback

Stop all processes sharing the upbrr database before restoring its complete matching backup and prior pinned image. Restore the previous Whackamole compose and config; legacy Upload Assistant URL and encrypted token are preserved. Do not remove or overwrite Upload Assistant's data/auth mounts or worker secrets.

Whackamole's `upload_assistant.backend: upbrr` selects its local CLI transport. `binary_path` and `config_path` select the installed fork. `allow_uploads: false` makes console runs debug/no-seed by default and never records a dry run as an upload. Site checks always suppress submission and client injection. To enable future explicitly requested real uploads, change `allow_uploads` in Whackamole Settings after reviewing dry-run output.

Do not commit config exports, backup archives, media, tokens or auth state.
