#!/usr/bin/env python3
"""anvil challenge importer: reads H7CTF'26 challenge.yml files and creates the
challenges via the admin API (schema: h7ctf26-challenge-schema.md).

  python3 tools/import_challenges.py ../H7CTF26 --dry-run
  ANVIL_ADMIN_PASSWORD=... python3 tools/import_challenges.py ../H7CTF26 --api http://localhost:8080 --user admin

deps: requests, pyyaml.
"""
from __future__ import annotations

import argparse
import getpass
import json
import os
import re
import sys
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

try:
    import yaml
except ImportError:
    sys.exit("error: pyyaml is required  (pip install pyyaml)")
try:
    import requests
except ImportError:
    sys.exit("error: requests is required  (pip install requests)")

DIFFICULTIES = {"easy", "medium", "hard", "insane"}
STATUSES = {"draft", "published", "archived"}
RESOURCE_TYPES = {"docker", "vm"}
INSTANCING = {"static", "on_demand"}
PROTOCOLS = {"tcp", "http"}
# mirrors allowedAttachmentExtensions in internal/api/handlers/attachment.go
ALLOWED_ATTACH_EXT = {
    ".zip", ".tar", ".gz", ".tgz", ".bz2", ".7z", ".rar", ".xz",
    ".pdf", ".txt", ".md",
    ".png", ".jpg", ".jpeg", ".gif", ".svg", ".webp",
    ".py", ".c", ".cpp", ".h", ".go", ".js", ".ts", ".sh", ".rb", ".java",
    ".pcap", ".pcapng", ".cap",
    ".bin", ".exe", ".elf", ".out",
    ".iso", ".img",
    ".json", ".xml", ".yaml", ".yml", ".toml", ".sql",
    ".rs", ".move", ".sol", ".vy", ".lock",
    ".asm", ".s", ".hpp", ".cc", ".cxx", ".hxx",
    ".html", ".htm", ".css", ".php",
    ".lua", ".pl", ".kt", ".swift", ".cs", ".sage",
    ".cfg", ".conf", ".ini", ".csv", ".env",
    "",  # no extension. big/exotic forensics artifacts go out zipped (.zip) or as an external-url handout.
}

_C = sys.stderr.isatty() and os.environ.get("NO_COLOR") is None
def _p(code: str, s: str) -> str: return f"\033[{code}m{s}\033[0m" if _C else s
def bold(s): return _p("1", s)
def red(s): return _p("31", s)
def green(s): return _p("32", s)
def yellow(s): return _p("33", s)
def dim(s): return _p("2", s)
def cyan(s): return _p("36", s)


class ValidationError(Exception):
    """a challenge.yml that violates the schema."""


def slugify(name: str) -> str:
    s = re.sub(r"[^a-z0-9]+", "-", name.lower()).strip("-")
    return re.sub(r"-{2,}", "-", s)


def strip_flag_prefix(prefix: str) -> str:
    """authors write "H7CTF{"; anvil wraps the uuid as <prefix>{<uuid>}, so the
    trailing brace must go or you get H7CTF{{uuid}}."""
    return prefix.strip().rstrip("{").rstrip("{}").rstrip("{")


@dataclass
class Flag:
    name: str
    flag_type: str            # static | dynamic
    value: str = ""           # static only
    dynamic_prefix: str = ""  # dynamic only (already brace-stripped)
    points: int = 0
    case_sensitive: bool = True
    is_regex: bool = False    # recorded for warnings; unsupported by the API

    @property
    def inlineable(self) -> bool:
        return self.case_sensitive


@dataclass
class Hint:
    content: str
    cost: int = 0


@dataclass
class Attachment:
    src: Path | None = None   # local file to upload; None for an external-url handout
    as_name: str = ""
    url: str = ""             # external handout (hosted off-platform, e.g. GCS bucket)
    sha256: str = ""


@dataclass
class Challenge:
    src_dir: Path
    yml_path: Path
    name: str
    slug: str
    author: str
    category: str
    difficulty: str
    description: str
    points: int
    status: str
    sub_description: str = ""
    flags: list[Flag] = field(default_factory=list)
    hints: list[Hint] = field(default_factory=list)
    attachments: list[Attachment] = field(default_factory=list)
    resource_type: str | None = None   # docker | vm | None (static-download)
    instancing: str = "on_demand"
    container_image: str = ""
    container_tag: str = ""
    cpu_limit: str = ""
    memory_limit: str = ""
    exposed_ports: list[dict] = field(default_factory=list)
    services: list[dict] = field(default_factory=list)  # multi-container roles; empty = single image
    instance_timeout: int | None = None
    max_extensions: int | None = None
    ova_path: Path | None = None
    warnings: list[str] = field(default_factory=list)

    @property
    def mode(self) -> str:
        if self.resource_type == "vm":
            return "vm/ova (on_demand)"
        if self.resource_type == "docker" and self.instancing == "on_demand":
            return "docker (on_demand instancer)"
        if self.resource_type == "docker" and self.instancing == "static":
            return "docker (always-on shared → imported no-container)"
        return "static download (no service)"

    @property
    def is_vm(self) -> bool:
        return self.resource_type == "vm"

    @property
    def is_instancer(self) -> bool:
        return self.resource_type == "docker" and self.instancing == "on_demand"


def _req(d: dict, key: str, where: str) -> Any:
    if key not in d or d[key] in (None, ""):
        raise ValidationError(f"{where}: missing required field '{key}'")
    return d[key]


def parse_flags(doc: dict, chal_points: int, where: str) -> list[Flag]:
    has_short = "flag" in doc and doc["flag"] not in (None, "")
    has_full = "flags" in doc and doc["flags"]
    if has_short and has_full:
        raise ValidationError(f"{where}: use either 'flag:' (shorthand) or 'flags:' — not both")
    if not has_short and not has_full:
        raise ValidationError(f"{where}: no flags — set 'flag:' or a 'flags:' list")

    if has_short:
        return [Flag(name="flag", flag_type="static", value=str(doc["flag"]), points=chal_points)]

    out: list[Flag] = []
    raw = doc["flags"]
    if not isinstance(raw, list):
        raise ValidationError(f"{where}: 'flags' must be a list")
    for i, f in enumerate(raw):
        if not isinstance(f, dict):
            raise ValidationError(f"{where}: flags[{i}] must be a mapping")
        ftype = str(f.get("type", "static")).lower()
        if ftype not in ("static", "dynamic"):
            raise ValidationError(f"{where}: flags[{i}].type must be static|dynamic")
        name = str(f.get("name") or (f"flag-{i+1}" if len(raw) > 1 else "flag"))
        pts = int(f.get("points") or chal_points)
        cs = bool(f.get("case_sensitive", True))
        rgx = bool(f.get("is_regex", False))
        if ftype == "static":
            val = f.get("value") or f.get("flag")  # 'flag' accepted as an alias for 'value'
            if val in (None, ""):
                raise ValidationError(f"{where}: flags[{i}] type static requires 'value' (or 'flag')")
            out.append(Flag(name, "static", value=str(val), points=pts,
                            case_sensitive=cs, is_regex=rgx))
        else:  # dynamic
            if f.get("value"):
                raise ValidationError(f"{where}: flags[{i}] type dynamic must omit 'value'")
            prefix = f.get("dynamic_flag_prefix")
            if not prefix:
                raise ValidationError(f"{where}: flags[{i}] type dynamic requires 'dynamic_flag_prefix'")
            out.append(Flag(name, "dynamic", dynamic_prefix=strip_flag_prefix(str(prefix)),
                            points=pts, case_sensitive=cs, is_regex=rgx))
    return out


def parse_hints(doc: dict, where: str) -> list[Hint]:
    # accepts: hints: ["text", ...]  (free, cost 0)
    #      or: hints: [{content|text: "...", cost: N}, ...]
    raw = doc.get("hints") or []
    if not isinstance(raw, list):
        raise ValidationError(f"{where}: 'hints' must be a list")
    out: list[Hint] = []
    for i, item in enumerate(raw):
        if isinstance(item, str):
            content, cost = item, 0
        elif isinstance(item, dict):
            content = str(item.get("content") or item.get("text") or "").strip()
            cost = int(item.get("cost") or 0)
        else:
            raise ValidationError(f"{where}: hints[{i}] must be a string or a mapping")
        if not content:
            raise ValidationError(f"{where}: hints[{i}] has no content")
        if cost < 0:
            raise ValidationError(f"{where}: hints[{i}] cost must be >= 0")
        out.append(Hint(content=content, cost=cost))
    return out


def parse_deploy(doc: dict, src_dir: Path, ch: Challenge, where: str) -> None:
    dep = doc.get("deploy")
    if not dep:
        return  # static-download challenge
    if not isinstance(dep, dict):
        raise ValidationError(f"{where}: 'deploy' must be a mapping")

    rt = str(_req(dep, "resource_type", where)).lower()
    if rt not in RESOURCE_TYPES:
        raise ValidationError(f"{where}: deploy.resource_type must be docker|vm")
    ch.resource_type = rt

    inst = str(dep.get("instancing", "on_demand")).lower()
    if inst not in INSTANCING:
        raise ValidationError(f"{where}: deploy.instancing must be static|on_demand")
    ch.instancing = inst

    if dep.get("network_mode"):
        ch.warnings.append(f"deploy.network_mode='{dep['network_mode']}' — no Anvil field; ignored")

    if rt == "docker":
        image = str(_req(dep, "image", where))
        registry = dep.get("registry")
        # anvil has no registry field; fold it into the image ref
        ch.container_image = f"{registry}/{image}" if registry else image
        ch.container_tag = str(dep.get("tag") or "")
        if dep.get("cpu_limit") is not None:
            ch.cpu_limit = str(dep["cpu_limit"])
        if dep.get("memory_limit") is not None:
            ch.memory_limit = str(dep["memory_limit"])
        for p in dep.get("exposed_ports", []) or []:
            proto = str(p.get("protocol", "tcp")).lower()
            if proto not in PROTOCOLS:
                raise ValidationError(f"{where}: exposed_ports protocol must be tcp|http")
            # derive the instancer routing service. http -> web route; tcp ->
            # raw-tls: web3 category gets its own web3 subdomain, everything else
            # is pwn. keeps challenge.yml protocol-only (frozen schema).
            svc = proto
            if proto == "tcp" and ch.category.strip().lower() == "web3":
                svc = "web3"
            ch.exposed_ports.append({"port": int(_req(p, "port", where)),
                                     "protocol": proto, "service": svc})
        # optional multi-container (compose-style) roles. when present, each role
        # is one pod; only public roles get a route. single-image challenges omit
        # this and behave exactly as before.
        def _svc_of(proto: str) -> str:
            if proto == "tcp" and ch.category.strip().lower() == "web3":
                return "web3"
            return proto
        for s in dep.get("services", []) or []:
            if not isinstance(s, dict):
                raise ValidationError(f"{where}: each deploy.services entry must be a mapping")
            name = str(_req(s, "name", where))
            if not re.fullmatch(r"[a-z0-9]([a-z0-9-]*[a-z0-9])?", name):
                raise ValidationError(f"{where}: service name '{name}' must be lowercase DNS-safe (a-z0-9-)")
            cmd = s.get("command") or []
            if cmd and not isinstance(cmd, list):
                raise ValidationError(f"{where}: service '{name}' command must be a list")
            svc_ports = []
            for p in s.get("ports", []) or []:
                proto = str(p.get("protocol", "tcp")).lower()
                if proto not in PROTOCOLS:
                    raise ValidationError(f"{where}: service '{name}' port protocol must be tcp|http")
                svc_ports.append({"port": int(_req(p, "port", where)),
                                  "protocol": proto, "service": _svc_of(proto)})
            env = s.get("env") or {}
            if env and not isinstance(env, dict):
                raise ValidationError(f"{where}: service '{name}' env must be a mapping")
            ch.services.append({
                "name": name,
                "image": str(s.get("image") or ""),
                "tag": str(s.get("tag") or ""),
                "command": [str(x) for x in cmd],
                "public": bool(s.get("public", False)),
                "egress": bool(s.get("egress", False)),
                "ports": svc_ports,
                "env": {str(k): str(v) for k, v in env.items()},
                "cpu_limit": str(s.get("cpu_limit") or ""),
                "memory_limit": str(s.get("memory_limit") or ""),
            })
        if ch.services:
            public = [s for s in ch.services if s["public"]]
            if not public:
                raise ValidationError(f"{where}: multi-container deploy needs at least one service with public: true")
            # the exposed_ports column mirrors the public roles' ports (drives the UI/detail)
            ch.exposed_ports = [dict(p) for s in public for p in s["ports"]]
        if dep.get("instance_timeout") is not None:
            ch.instance_timeout = int(dep["instance_timeout"])
        if dep.get("max_extensions") is not None:
            ch.max_extensions = int(dep["max_extensions"])
        if inst == "static":
            ch.warnings.append(
                "instancing=static → imported as a NO-CONTAINER challenge (no per-user "
                "instancer). Run the one shared instance via ops and put the endpoint "
                f"in the description. (image would be: {ch.container_image or '?'}"
                + (f", ports {[p['port'] for p in ch.exposed_ports]}" if ch.exposed_ports else "") + ")")
    else:  # vm
        ova = _req(dep, "ova", where)
        p = (src_dir / str(ova)).resolve()
        ch.ova_path = p
        if not p.exists():
            ch.warnings.append(f"OVA file not found: {p} (required for real import)")


def load_challenge(yml_path: Path) -> Challenge:
    where = str(yml_path)
    try:
        doc = yaml.safe_load(yml_path.read_text())
    except yaml.YAMLError as e:
        raise ValidationError(f"{where}: invalid YAML: {e}")
    if not isinstance(doc, dict):
        raise ValidationError(f"{where}: top level must be a mapping")

    src_dir = yml_path.parent
    name = str(_req(doc, "name", where))
    difficulty = str(_req(doc, "difficulty", where)).lower()
    if difficulty not in DIFFICULTIES:
        raise ValidationError(f"{where}: difficulty must be one of {sorted(DIFFICULTIES)}")
    status = str(doc.get("status", "draft")).lower()
    if status not in STATUSES:
        raise ValidationError(f"{where}: status must be one of {sorted(STATUSES)}")

    author = doc.get("author")
    if not author:
        raise ValidationError(f"{where}: missing required field 'author'")
    author = ", ".join(str(a) for a in author) if isinstance(author, list) else str(author)

    points = doc.get("points")
    if points is None:
        raise ValidationError(f"{where}: missing required field 'points'")
    points = int(points)
    if points < 1:
        raise ValidationError(f"{where}: points must be >= 1")

    description = str(_req(doc, "description", where))
    slug = str(doc.get("slug") or slugify(src_dir.name if src_dir.name else name))

    sub_description = str(doc.get("sub_description") or "").strip()
    ch = Challenge(
        src_dir=src_dir, yml_path=yml_path, name=name, slug=slug, author=author,
        category=str(_req(doc, "category", where)), difficulty=difficulty,
        description=description, points=points, status=status, sub_description=sub_description,
    )
    # blockchain challenges present under the "web3" category + web3 raw-tls route
    # (its own subdomain, not "pwn"). challenge.yml keeps category: blockchain.
    if ch.category.strip().lower() == "blockchain":
        ch.category = "web3"
    if sub_description and ("{" in sub_description or len(sub_description) > 200):
        ch.warnings.append("sub_description is long or contains '{' — keep it a short, plain, non-spoiler blurb "
                          "(it's always visible pre-launch; must not leak the flag)")
    ch.flags = parse_flags(doc, points, where)
    ch.hints = parse_hints(doc, where)
    parse_deploy(doc, src_dir, ch, where)

    for i, item in enumerate(doc.get("provide", []) or []):
        if not isinstance(item, dict) or "path" not in item:
            raise ValidationError(f"{where}: provide[{i}] must be a mapping with 'path'")
        src = (src_dir / str(item["path"])).resolve()
        as_name = str(item.get("as") or src.name)
        if not src.exists():
            ch.warnings.append(f"handout not found: {src}")
        ext = os.path.splitext(as_name)[1].lower()
        if ext not in ALLOWED_ATTACH_EXT:
            ch.warnings.append(f"handout '{as_name}' has ext '{ext}' not in Anvil's allowlist — upload will be rejected")
        ch.attachments.append(Attachment(src=src, as_name=as_name))

    # handout: block — external URLs (hosted off-platform, e.g. GCS bucket) or local files.
    for i, item in enumerate(doc.get("handout", []) or []):
        if not isinstance(item, dict):
            raise ValidationError(f"{where}: handout[{i}] must be a mapping")
        name = str(item.get("name") or "").strip()
        url = str(item.get("url") or "").strip()
        sha = str(item.get("sha256") or "").strip()
        if url:
            if not name:
                name = url.rsplit("/", 1)[-1]
            ch.attachments.append(Attachment(src=None, as_name=name, url=url, sha256=sha))
            continue
        # local handout: look in the challenge dir, then a handout/ subdir
        rel = str(item.get("path") or name)
        if not rel:
            raise ValidationError(f"{where}: handout[{i}] needs a url, path, or name")
        src = (src_dir / rel).resolve()
        if not src.exists():
            alt = (src_dir / "handout" / rel).resolve()
            if alt.exists():
                src = alt
        as_name = str(item.get("as") or name or src.name)
        if not src.exists():
            ch.warnings.append(f"handout not found: {src}")
        ext = os.path.splitext(as_name)[1].lower()
        if ext not in ALLOWED_ATTACH_EXT:
            ch.warnings.append(f"handout '{as_name}' has ext '{ext}' not in Anvil's allowlist — upload will be rejected")
        ch.attachments.append(Attachment(src=src, as_name=as_name))

    if "{{" in description:
        ch.warnings.append("description contains Jinja templating ({{ nc }}/{{ link }}); "
                           "Anvil does not substitute it — confirm the endpoint is written literally")
    for fl in ch.flags:
        if fl.is_regex:
            ch.warnings.append(f"flag '{fl.name}' is_regex=true is not supported by the Anvil flag API; "
                              "imported as an exact match")
    return ch


def discover(path: Path) -> list[Path]:
    if path.is_file():
        return [path]
    direct = path / "challenge.yml"
    if direct.exists():
        return [direct]
    found = sorted(path.rglob("challenge.yml"))
    if not found:
        found = sorted(path.rglob("challenge.yaml"))
    return found


class Anvil:
    def __init__(self, base: str, verbose: bool = False):
        self.base = base.rstrip("/")
        self.s = requests.Session()
        self.token: str | None = None
        self.verbose = verbose

    def _url(self, p: str) -> str:
        return f"{self.base}/api/v1{p}"

    def _hdr(self) -> dict:
        return {"Authorization": f"Bearer {self.token}"} if self.token else {}

    def login(self, user: str, password: str) -> None:
        r = self.s.post(self._url("/auth/login"),
                        json={"username": user, "password": password}, timeout=30)
        if r.status_code != 200:
            raise RuntimeError(f"login failed ({r.status_code}): {r.text[:300]}")
        self.token = r.json().get("access_token")
        if not self.token:
            raise RuntimeError(f"login ok but no access_token in response: {r.text[:200]}")

    def existing_slugs(self) -> set[str]:
        r = self.s.get(self._url("/admin/challenges"), headers=self._hdr(), timeout=30)
        if r.status_code != 200:
            return set()
        data = r.json()
        rows = data if isinstance(data, list) else data.get("challenges", [])
        out = set()
        for c in rows:
            if c.get("slug"):
                out.add(c["slug"])
            if c.get("name"):
                out.add(slugify(c["name"]))
        return out

    def create_challenge(self, ch: Challenge) -> str:
        body: dict[str, Any] = {
            "name": ch.name,
            "description": ch.description,
            "sub_description": ch.sub_description,
            "difficulty": ch.difficulty,
            "category": ch.category,
            "author_name": ch.author,
            "base_points": ch.points,
            "resource_type": "docker",
            "challenge_type": "docker",
            "flags": [self._inline_flag(f) for f in ch.flags if f.inlineable],
        }
        if ch.is_instancer:
            body["container_image"] = ch.container_image
            if ch.container_tag:
                body["container_tag"] = ch.container_tag
            if ch.cpu_limit:
                body["cpu_limit"] = ch.cpu_limit
            if ch.memory_limit:
                body["memory_limit"] = ch.memory_limit
            if ch.exposed_ports:
                body["exposed_ports"] = ch.exposed_ports
            if ch.services:
                body["services"] = ch.services
            if ch.instance_timeout is not None:
                body["instance_timeout"] = ch.instance_timeout
            if ch.max_extensions is not None:
                body["max_extensions"] = ch.max_extensions
        r = self.s.post(self._url("/admin/challenges"), headers=self._hdr(),
                        json=body, timeout=60)
        if r.status_code not in (200, 201):
            raise RuntimeError(f"create failed ({r.status_code}): {r.text[:400]}")
        return r.json()["id"]

    def create_ova(self, ch: Challenge) -> str:
        if not ch.ova_path or not ch.ova_path.exists():
            raise RuntimeError(f"OVA file missing: {ch.ova_path}")
        data = {
            "name": ch.name,
            "description": ch.description,
            "sub_description": ch.sub_description,
            "difficulty": ch.difficulty,
            "base_points": str(ch.points),
            "category": ch.category,
            "flags": "",  # added afterward via create_flag (multipart path can't do dynamic/case)
        }
        size = ch.ova_path.stat().st_size
        print(dim(f"      uploading OVA {ch.ova_path.name} ({size/1e9:.2f} GB)…"))
        with open(ch.ova_path, "rb") as fh:
            files = {"file": (ch.ova_path.name, fh, "application/octet-stream")}
            r = self.s.post(self._url("/admin/challenges/ova"), headers=self._hdr(),
                            data=data, files=files, timeout=None)
        if r.status_code not in (200, 201):
            raise RuntimeError(f"OVA create failed ({r.status_code}): {r.text[:400]}")
        return r.json()["id"]

    @staticmethod
    def _inline_flag(f: Flag) -> dict:
        d = {"name": f.name, "points": f.points, "flag_type": f.flag_type}
        if f.flag_type == "dynamic":
            d["dynamic_flag_prefix"] = f.dynamic_prefix
        else:
            d["flag"] = f.value
        return d

    def create_flag(self, chal_id: str, f: Flag) -> None:
        body: dict[str, Any] = {
            "name": f.name,
            "points": f.points,
            "flag_type": f.flag_type,
            "case_sensitive": f.case_sensitive,
        }
        if f.flag_type == "dynamic":
            body["dynamic_flag_prefix"] = f.dynamic_prefix
        else:
            body["flag"] = f.value
        r = self.s.post(self._url(f"/admin/challenges/{chal_id}/flags"),
                        headers=self._hdr(), json=body, timeout=30)
        if r.status_code not in (200, 201):
            raise RuntimeError(f"flag '{f.name}' failed ({r.status_code}): {r.text[:300]}")

    def create_hint(self, chal_id: str, h: Hint) -> None:
        r = self.s.post(self._url(f"/admin/challenges/{chal_id}/hints"),
                        headers=self._hdr(), json={"content": h.content, "cost": h.cost}, timeout=30)
        if r.status_code not in (200, 201):
            raise RuntimeError(f"hint failed ({r.status_code}): {r.text[:300]}")

    def upload_attachment(self, chal_id: str, a: Attachment, order: int) -> None:
        # external handout: register the URL (download redirects to it), no upload
        if a.url:
            r = self.s.post(self._url(f"/admin/challenges/{chal_id}/attachments/link"),
                            headers=self._hdr(),
                            json={"name": a.as_name, "url": a.url, "sha256": a.sha256, "sort_order": order},
                            timeout=60)
            if r.status_code not in (200, 201):
                raise RuntimeError(f"handout link '{a.as_name}' failed ({r.status_code}): {r.text[:300]}")
            return
        if not a.src or not a.src.exists():
            raise RuntimeError(f"attachment missing: {a.src}")
        with open(a.src, "rb") as fh:
            files = {"file": (a.as_name, fh, "application/octet-stream")}
            r = self.s.post(self._url(f"/admin/challenges/{chal_id}/attachments"),
                            headers=self._hdr(), data={"sort_order": str(order)},
                            files=files, timeout=300)
        if r.status_code not in (200, 201):
            raise RuntimeError(f"attachment '{a.as_name}' failed ({r.status_code}): {r.text[:300]}")

    def publish(self, chal_id: str) -> None:
        r = self.s.post(self._url(f"/admin/challenges/{chal_id}/publish"),
                        headers=self._hdr(), timeout=30)
        if r.status_code not in (200, 201):
            raise RuntimeError(f"publish failed ({r.status_code}): {r.text[:300]}")


def flag_summary(flags: list[Flag]) -> str:
    parts = []
    for f in flags:
        if f.flag_type == "dynamic":
            parts.append(f"dynamic({f.dynamic_prefix}{{…}})")
        else:
            parts.append("static" + ("" if f.case_sensitive else ",ci"))
    return ", ".join(parts)


def print_plan(ch: Challenge) -> None:
    print(f"  {bold(ch.name)}  {dim('['+ch.slug+']')}")
    print(f"      {dim('category')} {ch.category}   {dim('difficulty')} {ch.difficulty}"
          f"   {dim('points')} {ch.points}   {dim('status')} {ch.status}")
    print(f"      {dim('mode')} {cyan(ch.mode)}")
    if ch.sub_description:
        print(f"      {dim('sub_desc')} {ch.sub_description}")
    if ch.is_instancer:
        img = ch.container_image + (f":{ch.container_tag}" if ch.container_tag else "")
        ports = ", ".join(f"{p['port']}/{p['protocol']}" for p in ch.exposed_ports) or "-"
        print(f"      {dim('image')} {img}   {dim('ports')} {ports}"
              f"   {dim('cpu')} {ch.cpu_limit or '-'}   {dim('mem')} {ch.memory_limit or '-'}")
        if ch.instance_timeout is not None:
            print(f"      {dim('timeout')} {ch.instance_timeout}s   {dim('max_ext')} {ch.max_extensions}")
        if ch.services:
            print(f"      {dim('services')} {len(ch.services)} (multi-container):")
            for s in ch.services:
                sp = ",".join(f"{p['port']}/{p['protocol']}" for p in s["ports"]) or "-"
                tags = " ".join(t for t in [cyan("public") if s["public"] else "", "egress" if s["egress"] else ""] if t)
                cmd = " ".join(s["command"]) if s["command"] else "-"
                print(f"        - {bold(s['name'])}  {dim('ports')} {sp}  {dim('cmd')} {cmd}  {tags}")
    if ch.is_vm:
        exists = ch.ova_path and ch.ova_path.exists()
        size = f" ({ch.ova_path.stat().st_size/1e9:.2f} GB)" if exists else ""
        print(f"      {dim('ova')} {ch.ova_path}{size} {'' if exists else red('[MISSING]')}")
    print(f"      {dim('flags')} {len(ch.flags)}: {flag_summary(ch.flags)}")
    if ch.hints:
        print(f"      {dim('hints')} {len(ch.hints)}: " + ", ".join(f"{h.cost}pts" for h in ch.hints))
    if ch.attachments:
        att = ", ".join(
            (f"{a.as_name} {dim('(url)')}" if a.url
             else f"{a.as_name}{'' if (a.src and a.src.exists()) else red('[MISSING]')}")
            for a in ch.attachments
        )
        print(f"      {dim('handouts')} {att}")
    for w in ch.warnings:
        print(f"      {yellow('! ' + w)}")


def import_one(api: Anvil, ch: Challenge, publish: bool) -> None:
    if ch.is_vm:
        cid = api.create_ova(ch)
        print(green(f"      created (vm) id={cid}"))
        for f in ch.flags:
            api.create_flag(cid, f)
        print(green(f"      + {len(ch.flags)} flag(s) via API"))
    else:
        cid = api.create_challenge(ch)
        inline = [f for f in ch.flags if f.inlineable]
        api_flags = [f for f in ch.flags if not f.inlineable]
        print(green(f"      created id={cid}  ({len(inline)} inline flag(s))"))
        for f in api_flags:
            api.create_flag(cid, f)
        if api_flags:
            print(green(f"      + {len(api_flags)} case-insensitive flag(s) via API"))
    for h in ch.hints:
        api.create_hint(cid, h)
    if ch.hints:
        print(green(f"      + {len(ch.hints)} hint(s)"))
    for i, a in enumerate(ch.attachments):
        api.upload_attachment(cid, a, i)
    if ch.attachments:
        print(green(f"      + {len(ch.attachments)} handout(s)"))
    if publish or ch.status == "published":
        api.publish(cid)
        print(green("      published"))


def main() -> int:
    ap = argparse.ArgumentParser(description="Import H7CTF'26 challenge.yml files into Anvil.")
    ap.add_argument("path", help="repo dir, a challenge dir, or a challenge.yml")
    ap.add_argument("--api", default=os.environ.get("ANVIL_API", "http://localhost:8080"))
    ap.add_argument("--user", default=os.environ.get("ANVIL_ADMIN_USER", "admin"))
    ap.add_argument("--password", default=os.environ.get("ANVIL_ADMIN_PASSWORD"))
    ap.add_argument("--token", default=os.environ.get("ANVIL_ACCESS_TOKEN"),
                    help="use a pre-minted admin access token instead of username/password login")
    ap.add_argument("--dry-run", action="store_true", help="parse + validate + show the plan; no writes")
    ap.add_argument("--publish", action="store_true", help="publish every imported challenge (overrides status)")
    ap.add_argument("--only", help="comma-separated slugs/dir-names to import")
    ap.add_argument("--skip-existing", action="store_true", help="skip challenges whose slug already exists")
    ap.add_argument("--continue-on-error", action="store_true")
    ap.add_argument("--registry", default=os.environ.get("ANVIL_IMPORT_REGISTRY"),
                    help="prepend this registry/repo to each bare docker image ref "
                         "(e.g. an Artifact Registry path) so instances pull a real image")
    ap.add_argument("-v", "--verbose", action="store_true")
    args = ap.parse_args()

    root = Path(args.path).expanduser().resolve()
    if not root.exists():
        print(red(f"path not found: {root}")); return 2
    yml_files = discover(root)
    if not yml_files:
        print(red(f"no challenge.yml found under {root}")); return 2

    print(bold(f"Discovered {len(yml_files)} challenge file(s) under {root}\n"))
    challenges: list[Challenge] = []
    errors = 0
    only = {s.strip() for s in args.only.split(",")} if args.only else None
    for y in yml_files:
        try:
            ch = load_challenge(y)
        except ValidationError as e:
            print(red(f"  INVALID {y.parent.name}: {e}")); errors += 1
            continue
        if only and not ({ch.slug, ch.src_dir.name, slugify(ch.name)} & only):
            continue
        challenges.append(ch)

    if errors and not args.continue_on_error:
        print(red(f"\n{errors} file(s) failed validation. Fix them or pass --continue-on-error.")); return 1
    if not challenges:
        print(yellow("nothing to import (after filtering).")); return 0

    # --registry turns a bare logical image ref (e.g. "h7ctf26/perp_guard") into a
    # pullable registry path. Skip refs that already look qualified (host has a
    # dot/port, or already under this registry) so a full path is left untouched.
    if args.registry:
        reg = args.registry.rstrip("/")

        def _rewrite(img: str) -> str:
            if not img:
                return img
            first = img.split("/", 1)[0]
            if "." in first or ":" in first or img.startswith(reg + "/"):
                return img
            return f"{reg}/{img}"

        for ch in challenges:
            ch.container_image = _rewrite(ch.container_image)
            for s in ch.services:  # multi-container: rewrite per-service image overrides too
                s["image"] = _rewrite(s["image"])

    print(bold(f"\n=== Plan ({len(challenges)} challenge(s)) ==="))
    for ch in challenges:
        print_plan(ch)

    if args.dry_run:
        n_warn = sum(len(c.warnings) for c in challenges)
        print(bold(f"\nDRY RUN — nothing written. {len(challenges)} ready, "
                   f"{errors} invalid, {n_warn} warning(s)."))
        return 0

    api = Anvil(args.api, verbose=args.verbose)
    if args.token:
        api.token = args.token
        print(green("\nUsing provided access token."))
    else:
        password = args.password or getpass.getpass(f"admin password for {args.user}@{args.api}: ")
        try:
            api.login(args.user, password)
        except Exception as e:
            print(red(f"auth: {e}")); return 1
        print(green(f"\nAuthenticated as {args.user}."))

    existing = api.existing_slugs() if args.skip_existing else set()

    print(bold(f"\n=== Importing {len(challenges)} challenge(s) → {args.api} ==="))
    ok = skipped = failed = 0
    for ch in challenges:
        print(f"\n  {bold(ch.name)} {dim('['+ch.slug+']')}")
        if args.skip_existing and (ch.slug in existing or slugify(ch.name) in existing):
            print(yellow("      skip (already exists)")); skipped += 1
            continue
        try:
            import_one(api, ch, args.publish)
            ok += 1
        except Exception as e:
            print(red(f"      FAILED: {e}")); failed += 1
            if not args.continue_on_error:
                print(red("\nStopping (pass --continue-on-error to keep going).")); return 1

    print(bold(f"\n=== Done: {green(str(ok)+' imported')}, "
               f"{yellow(str(skipped)+' skipped')}, {red(str(failed)+' failed')} ==="))
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
