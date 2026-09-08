# example-notes — reference Attack-Defense service + checker

A minimal vulnerable "notes" service and its checker, meant as the template
challenge authors copy when adding an AD service to the H7CTF finals engine.

- `server.py` — the vulnerable service (line-based TCP, stdlib only)
- `checker.py` — the engine-facing checker (implements the JSON protocol)
- `Dockerfile` — how the service ships to a team's vulnbox

## Checker protocol

The game engine runs the checker once per action, as an external subprocess.
It writes ONE JSON task to the checker's stdin and reads ONE JSON verdict from
its stdout:

```
stdin :  {"action": "place" | "check", "host": "<ip|hostname>", "port": <int>, "flag": "<string>"}
stdout:  {"status": "OK" | "DOWN" | "FAULTY" | "FLAG_NOT_FOUND" | "RECOVERING", "message": "<short string>"}
```

- `place`: connect to `host:port` and store `flag` in the service the way a
  normal user would (here: save it as a note). Return `OK` once stored.
- `check`: connect, confirm the service behaves correctly **and** that the flag
  placed on an earlier tick is still retrievable.

Status meanings:

| status           | meaning                                                        |
|------------------|----------------------------------------------------------------|
| `OK`             | healthy; for `check`, the flag is present and correct          |
| `FLAG_NOT_FOUND` | service is up but the flag is gone or altered                  |
| `DOWN`           | unreachable, connection dropped, crashed, or timed out         |
| `FAULTY`         | responds but misbehaves (wrong protocol, bad data)             |
| `RECOVERING`     | up but not yet ready to serve (not used by this reference)     |

Hard rules enforced by the engine (`internal/game/checker.go`):

- Exit code **0** on every run, including failure verdicts.
- A non-zero exit, a timeout, or absent/garbled stdout is treated as `DOWN`.
- Output that isn't a single valid JSON object is treated as `FAULTY`.

So the checker must **catch every error path** and still print one JSON object.
`checker.py` does this: connection problems become `DOWN`, protocol surprises
become `FAULTY`, and any unexpected exception falls back to `FAULTY` — it never
crashes or exits non-zero.

## The service

`H7-NOTES` is a line-based TCP server. One command per line, one response line
per command:

```
REGISTER <user> <pass>   -> OK registered | ERR user exists
AUTH <user> <pass>       -> OK auth | ERR bad credentials
SET <key> <value...>     -> OK id=<n>                     (requires AUTH)
GET <key>                -> OK <value> | ERR not found    (requires AUTH)
LIST                     -> OK <key> <key> ...            (requires AUTH)
FETCH <id>               -> OK <value> | ERR not found
QUIT                     -> OK bye, then close
```

On connect the server sends a banner line `H7-NOTES v1`. Notes are private to
their owner and addressed by `key` after authentication; each note also gets a
sequential global integer `id` when first created.

## The intended vulnerability (IDOR)

**One deliberate flaw**, marked in `server.py` at the `FETCH` handler.

`GET` is correctly scoped: it only returns notes owned by the authenticated
user. `FETCH <id>` was added as a "share by permalink" shortcut but it

- requires **no authentication**, and
- performs **no ownership check** — it returns any note by its global id.

Because ids are handed out sequentially starting at 1, an attacker enumerates
`FETCH 1`, `FETCH 2`, … and reads every user's notes, including planted flags.

Exploit, start to finish:

```
$ nc <victim-ip> 9001
H7-NOTES v1
FETCH 1
OK H7CTF{...someone-elses-flag...}
FETCH 2
OK H7CTF{...next-flag...}
```

The fix a defending team would apply: make `FETCH` require authentication and
verify the caller owns the note (or drop the command entirely and share via a
per-note capability token).

## Build and run

### Docker

```bash
cd services/example-notes
docker build -t h7-example-notes .
docker run -d --name notes -p 9001:9001 h7-example-notes
```

### Directly (no Docker)

```bash
PORT=9001 python3 server.py
```

`HOST` (default `0.0.0.0`) and `PORT` (default `9001`) are read from the env.

## Run the checker locally

The checker takes its task on stdin and prints its verdict on stdout:

```bash
FLAG='H7CTF{ABCDEFGH23456789ABCDEFGH23456789}'

# place, then check — both should print {"status": "OK", ...}
echo "{\"action\":\"place\",\"host\":\"127.0.0.1\",\"port\":9001,\"flag\":\"$FLAG\"}" | python3 checker.py
echo "{\"action\":\"check\",\"host\":\"127.0.0.1\",\"port\":9001,\"flag\":\"$FLAG\"}" | python3 checker.py

# a flag never placed -> FLAG_NOT_FOUND
echo '{"action":"check","host":"127.0.0.1","port":9001,"flag":"H7CTF{NEVERPLACED...}"}' | python3 checker.py

# nothing listening -> DOWN
echo "{\"action\":\"check\",\"host\":\"127.0.0.1\",\"port\":9099,\"flag\":\"$FLAG\"}" | python3 checker.py
```

Because `place` and `check` are separate processes that share no memory, the
checker derives the note's credentials and key deterministically from the flag
(`sha256(flag)` — see `creds()`), so `check` can re-authenticate as the same
user a later tick and read the note back.

## Writing a checker for your own service

Copy `checker.py` and keep this shape:

1. **Read the task, be defensive.** Parse the one JSON object from stdin; a
   malformed task is `FAULTY`, not a crash.
2. **Separate the failure modes.** Connection/timeout errors → `DOWN`; the
   service answering but breaking protocol → `FAULTY`; the flag being gone →
   `FLAG_NOT_FOUND`. This reference uses two exception types (`DownError`,
   `ServiceError`) plus a `FLAG_NOT_FOUND` return to keep them apart.
3. **Set timeouts on every socket op.** The engine also kills a slow checker,
   but you want a clean `DOWN` message, not a hang.
4. **Make `place` reproducible.** Derive any usernames/keys/passwords the
   `check` will need from the flag (a hash), since the two runs share nothing.
5. **`check` should test health first, then the flag.** Round-trip a throwaway
   value (see `health_probe`) so a broken service reads as `FAULTY`; only after
   that, confirm the planted flag is still present, else `FLAG_NOT_FOUND`.
6. **Always print exactly one JSON object and exit 0.** Wrap the whole body so
   no code path can escape without a verdict.

Then register the service in the engine with its `port` and a `checker_ref`
pointing at your executable checker.
