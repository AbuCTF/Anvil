# Challenge repository workflow

Anvil can ingest challenge metadata in bulk without treating an untrusted repository as executable platform configuration. The workflow keeps large artifacts and secrets out of import jobs while still making a repository the source of truth for ordinary challenge metadata.

## Recommended repository shape

```text
challenges/
  crypto/vouchsafe/
    README.md
    challenge.yaml
    handout/
    container/
  pwn/lockstep/
    README.md
    challenge.yaml
    handout/
    container/
anvil/
  categories.csv
  challenges.csv
```

`categories.csv` is imported first. `challenges.csv` then carries one row per stable challenge slug, including its title, category, difficulty, base points, author, delivery type, image reference, release state, and scoring model. Download the current templates from **Admin → Data → Import** so generated files always use supported columns.

## Delivery types

- `docker`: a registry image and one or more exposed ports.
- `static`: downloadable handouts with no runtime.
- `external`: an organizer-managed URL or OSINT target with no Anvil runtime.
- `vm`: a VM challenge whose image is uploaded and converted in **System → Templates**.
- Multi-service challenges are created or completed in the challenge editor because their service roles, internal network, and secret injection require a structured review.

Set `scoring_mode` to `flag` for normal submissions or `graded` for a trusted grader that reports a best score from 0 to 1. Relative grading can be created in bulk, but its grader secret and service wiring are completed in the challenge's **Grading** tab.

## Safe import sequence

1. Build and push challenge images to a trusted registry. Publish handouts in a release or approved object store.
2. Generate `categories.csv` and `challenges.csv` in CI from reviewed metadata.
3. Import categories in create-only mode and run the dry-run.
4. Fix or download every reported row issue. No data is written during preview.
5. Apply the category plan once, then repeat for challenges.
6. Open each imported challenge to add flags, hints, handouts, grader configuration, or a VM template.
7. Preview as a participant, run the challenge, and publish only after **Release checks** reports it ready.

Imports are limited to 5 MB and 5,000 rows. Apply is an idempotent serializable transaction: every row succeeds or the entire import rolls back. A stale preview returns a conflict and must be generated again. Challenges with activity are never overwritten by a merge import.

## What exports are for

The Anvil bundle includes portable JSON and CSV, a checksum manifest, event metadata, participants, challenge metadata, the scoreboard, solve records, submission outcomes, and Ledger balances/history. It excludes passwords, sessions, submitted flag values, grader secrets, runtime credentials, live instances, and binary handouts. Use infrastructure backups when a byte-for-byte disaster-recovery image is required.
