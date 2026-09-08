#!/usr/bin/env python3
"""Checker for the H7-NOTES reference service.

Reads one JSON task from stdin and writes one JSON verdict to stdout:
    in:  {"action": "place"|"check", "host": str, "port": int, "flag": str}
    out: {"status": "OK"|"DOWN"|"FAULTY"|"FLAG_NOT_FOUND"|"RECOVERING",
          "message": str}

place  stores the flag as a note under credentials derived from the flag.
check  re-derives those credentials, confirms basic service health, then
       confirms the planted flag is still retrievable.
"""

import hashlib
import json
import os
import socket
import sys

TIMEOUT = 5.0


class ServiceError(Exception):
    """Service reachable but misbehaving -> FAULTY."""


class DownError(Exception):
    """Service unreachable or connection broke -> DOWN."""


class Conn:
    def __init__(self, host, port):
        try:
            self.sock = socket.create_connection((host, port), timeout=TIMEOUT)
        except OSError as e:
            raise DownError(f"connect: {e}")
        self.sock.settimeout(TIMEOUT)
        self.buf = self.sock.makefile("rwb")

    def readline(self):
        try:
            line = self.buf.readline()
        except (OSError, socket.timeout) as e:
            raise DownError(f"read: {e}")
        if not line:
            raise DownError("connection closed")
        return line.decode("utf-8", "replace").rstrip("\r\n")

    def cmd(self, text):
        try:
            self.buf.write((text + "\n").encode())
            self.buf.flush()
        except OSError as e:
            raise DownError(f"write: {e}")
        return self.readline()

    def close(self):
        try:
            self.sock.close()
        except OSError:
            pass


def creds(flag):
    h = hashlib.sha256(flag.encode()).hexdigest()
    return f"chk_{h[:12]}", h[12:28], f"flag_{h[28:40]}"


def expect_banner(conn):
    banner = conn.readline()
    if not banner.startswith("H7-NOTES"):
        raise ServiceError(f"unexpected banner: {banner!r}")


def health_probe(conn):
    """Register a throwaway user and round-trip a canary note."""
    tag = os.urandom(6).hex()
    user, password, canary = f"probe_{tag}", tag, f"canary_{tag}"
    if conn.cmd(f"REGISTER {user} {password}") != "OK registered":
        raise ServiceError("register probe failed")
    if conn.cmd(f"AUTH {user} {password}") != "OK auth":
        raise ServiceError("auth probe failed")
    if not conn.cmd(f"SET k {canary}").startswith("OK id="):
        raise ServiceError("set probe failed")
    if conn.cmd("GET k") != f"OK {canary}":
        raise ServiceError("get probe returned wrong value")


def do_place(host, port, flag):
    user, password, key = creds(flag)
    conn = Conn(host, port)
    try:
        expect_banner(conn)
        reg = conn.cmd(f"REGISTER {user} {password}")
        if reg not in ("OK registered", "ERR user exists"):
            raise ServiceError(f"register: {reg!r}")
        if conn.cmd(f"AUTH {user} {password}") != "OK auth":
            raise ServiceError("auth after register failed")
        if not conn.cmd(f"SET {key} {flag}").startswith("OK id="):
            raise ServiceError("set flag failed")
        if conn.cmd(f"GET {key}") != f"OK {flag}":
            raise ServiceError("readback after set mismatch")
        return "OK", "flag placed"
    finally:
        conn.close()


def do_check(host, port, flag):
    user, password, key = creds(flag)
    conn = Conn(host, port)
    try:
        expect_banner(conn)
        health_probe(conn)

        auth = conn.cmd(f"AUTH {user} {password}")
        if auth != "OK auth":
            # Credentials derived from the flag are gone -> the placement
            # itself vanished (service reset / wiped), not a protocol fault.
            return "FLAG_NOT_FOUND", "flag owner missing"

        resp = conn.cmd(f"GET {key}")
        if resp == f"OK {flag}":
            return "OK", "flag present"
        if resp == "ERR not found" or resp.startswith("OK "):
            return "FLAG_NOT_FOUND", "flag missing or altered"
        raise ServiceError(f"unexpected GET response: {resp!r}")
    finally:
        conn.close()


def main():
    try:
        task = json.load(sys.stdin)
        action = task["action"]
        host = task["host"]
        port = int(task["port"])
        flag = task["flag"]
    except (ValueError, KeyError, TypeError) as e:
        print(json.dumps({"status": "FAULTY", "message": f"bad task: {e}"}))
        return 0

    try:
        if action == "place":
            status, message = do_place(host, port, flag)
        elif action == "check":
            status, message = do_check(host, port, flag)
        else:
            status, message = "FAULTY", f"unknown action: {action}"
    except DownError as e:
        status, message = "DOWN", str(e)
    except ServiceError as e:
        status, message = "FAULTY", str(e)
    except Exception as e:  # any other failure is a service defect, not a crash verdict
        status, message = "FAULTY", f"checker error: {e}"

    print(json.dumps({"status": status, "message": message}))
    return 0


if __name__ == "__main__":
    sys.exit(main())
