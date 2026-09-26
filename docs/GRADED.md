# Graded challenges

A graded challenge has no flag. Each team's instance runs a **grader** in an
internal role that players can't reach. Players submit solutions through the
challenge's own portal; for each one the grader asks Anvil for leave to run it
(`/evaluate`, where the ledger charges), runs it, and reports a score in `[0,1]`
(`/report`). Anvil keeps every team's best score; the team earns
`round(best × base_points)` points (in economy mode the ledger prices it).

Switch a challenge to graded in the admin editor (Settings → Scoring → Graded)
or with `"scoring_mode": "graded"` on the admin challenge API. The Grading tab
shows the challenge secret, the grader env, and the recent evaluations.

## Wiring

Graded challenges must be multi-container (`container_spec` / `services:`).
Put the grader on its own role with `public: false` and `egress: true` (it has
to reach Anvil). Anvil fills these placeholders in that role's env only:

| placeholder        | value                                                                  |
| ------------------ | ---------------------------------------------------------------------- |
| `${GRADER_SECRET}` | this instance's signing key: `hex(HMAC-SHA256(challenge secret, INSTANCE_ID))` |
| `${INSTANCE_ID}`   | this instance's id, sent as `X-Anvil-Instance`                         |
| `${ANVIL_TEAM_ID}` | the owning team's uuid, sent as `team_id`                              |
| `${GRADER_URL}`    | `https://ctf.h7tex.com/api/v1/graded/report` (evaluate is the sibling `/evaluate`) |

```yaml
services:
  - name: portal            # what players reach; must NOT reference GRADER_SECRET
    public: true
    env: { GRADER: "http://grader:9000" }
  - name: grader
    public: false
    egress: true
    env:
      GRADER_SECRET: "${GRADER_SECRET}"
      GRADER_URL: "${GRADER_URL}"
      ANVIL_TEAM_ID: "${ANVIL_TEAM_ID}"
      INSTANCE_ID: "${INSTANCE_ID}"
      CHALLENGE_SLUG: "deep-dive"   # literal: the challenge slug
```

The key is per instance: a key leaked out of one team's instance signs for that
instance only. A public role that references `${GRADER_SECRET}` is refused at
launch. Rotating the challenge secret invalidates every running grader's key;
restart the instances after a rotation.

## Signing (both endpoints)

| header              | value                                                              |
| ------------------- | ------------------------------------------------------------------ |
| `X-Anvil-Challenge` | challenge slug                                                     |
| `X-Anvil-Instance`  | `${INSTANCE_ID}`                                                   |
| `X-Anvil-Timestamp` | unix seconds; rejected when more than 120s off Anvil's clock       |
| `X-Anvil-Nonce`     | 16–64 random bytes, hex; single use across both endpoints         |
| `X-Anvil-Signature` | `hex(HMAC-SHA256(GRADER_SECRET, ts + "\n" + nonce + "\n" + sha256hex(body)))` |

The HMAC key is the `GRADER_SECRET` string as-is; `sha256hex` is lowercase hex
of the exact body bytes you send. Body limit 8KB. Every signed request spends
its nonce, even one that is then refused, so retry with a fresh nonce.

## POST /api/v1/graded/evaluate

```json
{"team_id": "<ANVIL_TEAM_ID>", "eval_id": "<1-64 chars, unique per challenge>"}
```

Exactly these two fields. Anvil checks, per team and challenge:

- `team_id` owns the instance in `X-Anvil-Instance`;
- economy on: the team has the challenge launched (`open` with its timer
  running) or already holds a score on it;
- no other evaluation still running (one in flight; an unreported one expires
  after 15 minutes and is refunded);
- 10 minutes since the previous evaluation.

Price, economy on: evaluations 1–3 after each launch are free; evaluation
`k ≥ 4` costs `round(0.2 × launch cost of the challenge's band × 1.5^(k-4))`
credits, charged now. Economy off: always free. `infra_error` and expired
evaluations are refunded and don't count toward `k` or the cooldown.

`200 {"eval_id": "...", "charged": 50, "evaluations_used": 4}`. Retrying the same
`eval_id` returns the same answer and charges nothing.

## POST /api/v1/graded/report

```json
{"team_id": "<ANVIL_TEAM_ID>", "eval_id": "...", "status": "ok",
 "score": 0.62, "idempotency_key": "<1-64 chars>", "raw": {"depth": 7, "of": 12}}
```

- `status`: `ok` (default) or `infra_error`. `score` (finite, `0..1`) is
  required for `ok`; `infra_error` refunds the evaluation and scores nothing.
- One verdict per evaluation, within 15 minutes of its `/evaluate`.
- Same `idempotency_key` again: no-op, returns the current best.
- `raw` (optional object, ≤4KB): stored with the team's best and shown to that
  team on the challenge page. Don't put anything in it the team mustn't see.

`200 {"best": 0.62, "improved": true}`, plus `"refunded": n` after a charged
`infra_error` and `"practice": true` after the event ends.

## Errors

Always `{"error": "..."}`; 429s also carry `retry_after` (seconds) and `Retry-After`.

| status | when |
| ------ | ---- |
| 400 | bad body / headers, score outside `[0,1]`, body over 8KB |
| 401 | missing headers, stale timestamp, bad signature |
| 402 | not enough credits for this evaluation |
| 403 | challenge not graded, unknown instance, `team_id` isn't the instance owner, not launched, event not started |
| 404 | unknown challenge, no such evaluation for this team |
| 409 | nonce reused, evaluation already closed/expired, `eval_id`/`idempotency_key` used by another team |
| 429 | evaluation in flight, cooldown, or more than 60 calls/min for one team |

## Event phases

Before the start both endpoints refuse (403). While live, scores count. After
the end evaluations are free practice and reports return `"practice": true`
without moving the best.

## Reference client (Python 3, stdlib only)

```python
import hashlib, hmac, json, os, secrets, time, urllib.error, urllib.request

BASE = os.environ["GRADER_URL"].rsplit("/", 1)[0]   # .../api/v1/graded
KEY = os.environ["GRADER_SECRET"].encode()          # used as-is, not hex-decoded
TEAM = os.environ["ANVIL_TEAM_ID"]

def call(path, payload):
    body = json.dumps(payload, separators=(",", ":")).encode()
    ts, nonce = str(int(time.time())), secrets.token_hex(16)
    msg = f"{ts}\n{nonce}\n{hashlib.sha256(body).hexdigest()}".encode()
    req = urllib.request.Request(f"{BASE}/{path}", data=body, method="POST", headers={
        "Content-Type": "application/json", "X-Anvil-Challenge": os.environ["CHALLENGE_SLUG"],
        "X-Anvil-Instance": os.environ["INSTANCE_ID"], "X-Anvil-Timestamp": ts,
        "X-Anvil-Nonce": nonce, "X-Anvil-Signature": hmac.new(KEY, msg, hashlib.sha256).hexdigest()})
    try:
        with urllib.request.urlopen(req, timeout=10) as r:
            return r.status, json.load(r)
    except urllib.error.HTTPError as e:
        return e.code, json.load(e)

def grade(run):  # run() -> score in [0,1] plus a small raw dict
    eval_id = secrets.token_hex(8)
    status, admit = call("evaluate", {"team_id": TEAM, "eval_id": eval_id})
    if status != 200:
        return status, admit                      # tell the player: cooldown, credits, ...
    try:
        score, raw = run()
        verdict = {"status": "ok", "score": score, "raw": raw}
    except Exception:
        verdict = {"status": "infra_error"}       # refunded, doesn't count
    return call("report", {"team_id": TEAM, "eval_id": eval_id, "idempotency_key": eval_id, **verdict})
```

To test by hand, derive an instance key from the challenge secret (Grading tab):
`printf %s "$INSTANCE_ID" | openssl dgst -sha256 -hmac "$CHALLENGE_SECRET" -r | cut -d' ' -f1`.
