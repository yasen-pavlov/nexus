# Backup Strategy

A 3-2-1 setup for the homelab and personal data.

## Rule

> 3 copies, on 2 different media, 1 copy offsite.

## What's backed up

| Data                              | Source              | Target                  | Frequency |
| --------------------------------- | ------------------- | ----------------------- | --------- |
| Postgres databases                | Proxmox VM          | NAS snapshot + Backblaze | Daily     |
| Paperless-ngx documents           | LXC container       | NAS snapshot + Backblaze | Daily     |
| Photos                            | Phone + Lightroom   | NAS + Backblaze B2      | Real-time |
| Configuration (dotfiles, compose) | Laptop              | Private Git + Backblaze | On change |
| Nextcloud user data               | LXC container       | NAS snapshot            | Hourly    |

## Postgres specifics

Each database ships a `pg_dump` to `/volume2/backups/postgres/$(date +%F)` at
02:00. Retention is 14 daily, 8 weekly, 12 monthly. A nightly restore test
spins up a disposable LXC, restores the latest dump, runs a sanity query, and
tears down — if the restore fails, a notification fires to the phone.

## Offsite

Backblaze B2 via `restic`. Encryption key lives in the password manager with a
paper copy in the fireproof box. Restic check runs monthly against a random
10% sample of snapshots.

## Recovery drills

Once a quarter I actually restore one of the big backups from cold storage to
a fresh machine and verify it boots. It has caught broken credentials twice.
