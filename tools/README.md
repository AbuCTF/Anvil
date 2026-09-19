# Anvil tools

## `import_challenges.py` — H7CTF'26 challenge importer

Reads `challenge.yml` files (frozen schema v1, see `h7ctf26-challenge-schema.md`)
and creates the challenges through Anvil's admin API. Deps: `requests`, `pyyaml`.

```bash
# dry run — validate the whole repo and print exactly what would happen (no auth, no writes)
python3 tools/import_challenges.py ../H7CTF26 --dry-run

# real import against local dev
ANVIL_ADMIN_PASSWORD=... python3 tools/import_challenges.py ../H7CTF26 \
    --api http://localhost:8080 --user admin

# one challenge, publish it, skip ones already present, keep going past errors
python3 tools/import_challenges.py ../H7CTF26/pwn/coatroom \
    --publish --skip-existing --continue-on-error
```

`path` may be a repo dir (recursively finds every `challenge.yml`), a single
challenge dir, or a `challenge.yml`. Flags: `--only slug,slug`, `--skip-existing`,
`--publish`, `--continue-on-error`, `-v`.

### How each schema mode maps to Anvil

| schema | Anvil |
|---|---|
| no `deploy:` (static download) | challenge with **no container** — handout only |
| `deploy` docker + `instancing: on_demand` | container fields → per-user **instancer** (Start Instance) |
| `deploy` docker + `instancing: static` | imported as **no-container**; run the one shared instance via ops and put the endpoint in the description |
| `deploy` `resource_type: vm` | `POST /admin/challenges/ova` (multipart OVA upload) |

Flags: `flag:` shorthand or a `flags:` list. Static flags carry a literal `value`;
dynamic flags carry `dynamic_flag_prefix` (Anvil issues `<prefix>{<uuid>}` per
instance and injects it into the container as `$FLAG`).

### Known limitations (surfaced as warnings at import time, nothing is silent)

- **`dynamic_flag_prefix`**: authors write `"H7CTF{"`; the importer strips the
  trailing brace so Anvil stores `H7CTF` and wraps the UUID as `H7CTF{<uuid>}`.
- **`case_sensitive: false`** is created via the per-flag API (not inline), so
  the challenge's stored `total_flags` display counter may undercount by those
  flags — solve-gating recomputes from the flags table, so scoring is unaffected.
- **`is_regex: true`** is not supported by any Anvil create path — imported as an
  exact match (warned).
- **`registry:`** has no Anvil field — folded into `container_image`
  (`ghcr.io/foo/bar`). **`network_mode:`** is ignored (warned).
- **Jinja `{{ nc }}` / `{{ link }}`** in descriptions are passed through
  verbatim; Anvil does not substitute them (warned).
- **VM/OVA dynamic flags** are added via the per-flag API after the multipart
  upload, since the OVA endpoint's inline-flag path doesn't store dynamic/case
  attributes.
