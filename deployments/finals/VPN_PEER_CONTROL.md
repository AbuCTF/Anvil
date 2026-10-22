# Finals VPN peer control contract

Finals terminates WireGuard on separate gateway hosts. The Anvil API must run with
`vpn.peer_control: external`; in this mode it never runs `wg` or `nsenter` locally.
The `vpn_configs` table is the desired peer source of truth.

The inventory publisher reads active rows as a complete snapshot:

- `public_key` is the WireGuard peer identity.
- `host(assigned_ip)` is installed as the peer's single `/32` AllowedIP.
- `user_id` is inventory metadata, not a WireGuard key.
- `is_active = true` includes the peer; false removes it on the next snapshot.
- `updated_at` orders snapshots; `private_key` must never leave Anvil.

A successful config creation commits one active desired row. Finals addresses are
allocated under a transaction lock from `10.80.<team_number>.200-239` for team
numbers 1-15. An address recorded in `vpn_peer_history` is never issued again;
users without an active non-NOP game team or a valid team number fail closed.
Regeneration atomically replaces `public_key` and `assigned_ip`, clears handshake
and byte counters, and updates `updated_at`. Revocation marks the row inactive and erases its
encrypted client private key. The next complete snapshot therefore adds the new
peer and removes an old, missing, or inactive peer.

`vpn_peer_history` is the append-only attribution ledger. Each `issued`, `rotated`,
or `revoked` event records `user_id`, `team_id`, `team_number`, `public_key`,
`assigned_ip`, and `occurred_at`; it never stores a private key. New private keys
exist at rest only in `vpn_configs.private_key_ciphertext`, encrypted with the
`vpn-config` scope. A legacy plaintext row is encrypted and cleared on first read.

The production publisher must serialize all active rows, sign the exact snapshot,
and write it owner-readable only. Each gateway reconciles the full verified set,
rejects duplicate keys or IPs, applies `assigned_ip/32`, removes peers absent from
the snapshot, and reports handshake and transfer counters back to the matching row.
Release remains blocked until create, regenerate, removal, signature rejection, and
gateway failover are exercised against this contract.

The controller polls `GET /api/v1/internal/arena/vpn/peers` over Anvil HTTPS using
`Authorization: Bearer $ANVIL_GAME_INTERNAL_TOKEN`. The response is a deterministic
`{"peers": [...]}` snapshot containing only `peer_id`, `team_id`, `team_number`,
`public_key`, and `address`; invalid or unattributed active rows fail the whole read.
