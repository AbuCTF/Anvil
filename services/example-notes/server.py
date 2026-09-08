#!/usr/bin/env python3
"""H7-NOTES: a minimal line-based notes service for Attack-Defense play.

Protocol (one command per line, one response line per command):
    REGISTER <user> <pass>   -> OK registered | ERR user exists
    AUTH <user> <pass>       -> OK auth | ERR bad credentials
    SET <key> <value...>     -> OK id=<n>            (requires AUTH)
    GET <key>                -> OK <value> | ERR not found   (requires AUTH)
    LIST                     -> OK <key> <key> ...   (requires AUTH)
    FETCH <id>               -> OK <value> | ERR not found
    QUIT                     -> closes the connection

Notes are private to the user who created them, reachable by key only after
authentication. Each note also gets a global integer id at creation time.
"""

import os
import socketserver
import threading

BANNER = "H7-NOTES v1"
MAX_LINE = 4096


class Store:
    def __init__(self):
        self._lock = threading.Lock()
        self._users = {}          # user -> pass
        self._notes = {}          # id -> (owner, key, value)
        self._by_owner = {}       # owner -> {key -> id}
        self._next_id = 1

    def register(self, user, password):
        with self._lock:
            if user in self._users:
                return False
            self._users[user] = password
            self._by_owner[user] = {}
            return True

    def authenticate(self, user, password):
        with self._lock:
            return user in self._users and self._users[user] == password

    def set_note(self, owner, key, value):
        with self._lock:
            keys = self._by_owner[owner]
            note_id = keys.get(key)
            if note_id is None:
                note_id = self._next_id
                self._next_id += 1
                keys[key] = note_id
            self._notes[note_id] = (owner, key, value)
            return note_id

    def get_by_key(self, owner, key):
        with self._lock:
            note_id = self._by_owner[owner].get(key)
            if note_id is None:
                return None
            return self._notes[note_id][2]

    def list_keys(self, owner):
        with self._lock:
            return list(self._by_owner[owner].keys())

    def get_by_id(self, note_id):
        with self._lock:
            note = self._notes.get(note_id)
            return note[2] if note else None


class Handler(socketserver.StreamRequestHandler):
    def setup(self):
        super().setup()
        self.user = None

    def reply(self, text):
        self.wfile.write((text + "\n").encode())
        self.wfile.flush()

    def handle(self):
        self.reply(BANNER)
        store = self.server.store
        while True:
            raw = self.rfile.readline(MAX_LINE)
            if not raw:
                return
            try:
                line = raw.decode("utf-8", "strict").rstrip("\r\n")
            except UnicodeDecodeError:
                self.reply("ERR bad encoding")
                continue
            if not line:
                self.reply("ERR empty")
                continue

            parts = line.split(" ", 1)
            cmd = parts[0].upper()
            rest = parts[1] if len(parts) > 1 else ""

            if cmd == "QUIT":
                self.reply("OK bye")
                return

            elif cmd == "REGISTER":
                args = rest.split(" ", 1)
                if len(args) != 2 or not args[0] or not args[1]:
                    self.reply("ERR usage: REGISTER <user> <pass>")
                elif store.register(args[0], args[1]):
                    self.reply("OK registered")
                else:
                    self.reply("ERR user exists")

            elif cmd == "AUTH":
                args = rest.split(" ", 1)
                if len(args) != 2:
                    self.reply("ERR usage: AUTH <user> <pass>")
                elif store.authenticate(args[0], args[1]):
                    self.user = args[0]
                    self.reply("OK auth")
                else:
                    self.reply("ERR bad credentials")

            elif cmd == "SET":
                args = rest.split(" ", 1)
                if self.user is None:
                    self.reply("ERR not authenticated")
                elif len(args) != 2 or not args[0]:
                    self.reply("ERR usage: SET <key> <value>")
                else:
                    note_id = store.set_note(self.user, args[0], args[1])
                    self.reply(f"OK id={note_id}")

            elif cmd == "GET":
                if self.user is None:
                    self.reply("ERR not authenticated")
                elif not rest:
                    self.reply("ERR usage: GET <key>")
                else:
                    value = store.get_by_key(self.user, rest)
                    self.reply(f"OK {value}" if value is not None else "ERR not found")

            elif cmd == "LIST":
                if self.user is None:
                    self.reply("ERR not authenticated")
                else:
                    self.reply("OK " + " ".join(store.list_keys(self.user)))

            elif cmd == "FETCH":
                # DELIBERATE VULNERABILITY (IDOR): fetch-by-id was meant as a
                # "share by permalink" shortcut, but it neither requires
                # authentication nor checks that the caller owns the note.
                # Note ids are sequential, so an attacker enumerates 1..N and
                # reads every user's notes, flags included.
                try:
                    note_id = int(rest)
                except ValueError:
                    self.reply("ERR usage: FETCH <id>")
                    continue
                value = store.get_by_id(note_id)
                self.reply(f"OK {value}" if value is not None else "ERR not found")

            else:
                self.reply("ERR unknown command")


class Server(socketserver.ThreadingTCPServer):
    allow_reuse_address = True
    daemon_threads = True

    def __init__(self, addr):
        super().__init__(addr, Handler)
        self.store = Store()


def main():
    host = os.environ.get("HOST", "0.0.0.0")
    port = int(os.environ.get("PORT", "9001"))
    with Server((host, port)) as srv:
        print(f"H7-NOTES listening on {host}:{port}", flush=True)
        srv.serve_forever()


if __name__ == "__main__":
    main()
