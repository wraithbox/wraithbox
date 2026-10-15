# S09-06 - CA Certificates

**Purpose:** The profile and scope of each TLS inspection CA, the leaf
certificates it signs, and its rotation.

## Design

### CA profile

- `basicConstraints` critical, with `CA:TRUE` and `pathLen:0`;
- `keyUsage` `keyCertSign` only;
- no extended key usage.

### Scope

Each VM has its own CA, and two from day 30 of each CA on
("Lifetimes"). So a leaf signed for one VM isn't trusted in another.

A CA is trusted **only inside guests**, never on the host.

The CA certificate has no name constraints.

#### Leaf issuance

What limits the names a CA vouches for is `wb-proxyd`. It signs a leaf
only:

- for the hostname of the stream, after the SNI check;
- when the VM's effective policy inspects that host
  (S07-04-stream-hand-off, and S07-05-hosts-and-modes, "Name binding"
  and "Modes, per host").

It signs with the CA of the stream's VM. It keys its leaf cache on VM,
CA and hostname.

A pass-mode stream is relayed unchanged, and `wb-proxyd` doesn't sign a
leaf for it. Any other stream that fails this rule is reset, and logged
with the rule.

#### Leaf profile

- Exactly one `dNSName` subject alternative name, equal to the stream's
  hostname. It has neither a wildcard nor an IP address name.
- `CA:FALSE`.
- Extended key usage `serverAuth`.
- A validity within the signing CA's.

#### Not on the host

`wb-proxyd`'s pool of roots for upstream connections never includes a
Wraith Box CA, whatever the host's trust store holds.

### Lifetimes

- Leaf certificates: 24 hours, cut to the signing CA's end, cached in
  memory.
- CA: 60 days, rotated with an overlap.

`wb-hostd` schedules each step. `wb-proxyd` issues, switches and
deletes key items.

#### Rotation

Days count from the current CA's issue.

1. *Day 30.* `wb-proxyd` issues the successor with a new key.
   `wb-guestd` installs it next to the current CA. `wb-hostd` records
   the install when `wb-guestd` reports it.
2. *Day 58.* `wb-proxyd` signs with the successor, but only once its
   install is recorded. Until then it refuses the VM's inspected
   streams, with the rule `ca-not-installed`. A guest that doesn't
   report the install only cuts off its own traffic.
3. *Day 60.* The old CA expires.
   - `wb-guestd` removes it from the trust files, and from the system
     trust store where the guest has one (S12-platforms).
   - Its key goes on the removal list (S15-least-privilege, "Declared
     access"): the item, or on Windows the named key. `wb-proxyd`
     deletes it.

`wb-proxyd` never signs with an expired CA. That is what revokes the
CA: a guest that keeps trusting it gets no leaf signed by it.

#### Overlap

A process that started before the successor was installed works until
the switch, 28 days later. Only a process that runs longer than that,
and never rereads its trust, fails, with a certificate error. Time the
VM spends saved counts toward that bound.

#### Boot, restore and host wake

On every boot, every restore from saved state and every host wake,
these run in this order:

1. `wb-guestd` sets the guest clock from `wb-hostd`
   (S06-06-devices-and-warm-start, "Time and sleep").
2. A CA that has expired is removed, and its key item goes on the
   removal list.
3. A step of the schedule that came due while the VM was off or the
   host slept runs.
4. If the record has no CA, or every CA in it has expired,
   `wb-proxyd` issues a new one.
5. `wb-guestd` installs the VM's current CAs from `wb-hostd`'s own
   record, never from what the guest reports it has.
6. `wb-hostd` records the install.
7. Only then does `wb-proxyd` accept the VM's streams.

A recorded CA whose key doesn't restore is refused with
`ca-key-restore` (S09-05-ca-keys, "Valid CA"). It doesn't lead to a new
one.

After a restore or a host wake, `wb-proxyd` keeps signing with the CA
it signed with before the save or the sleep. It does so also past day
58, while that CA is valid, because resumed processes loaded their
trust before it.

#### Audit

Each of these is a Device Config State Change (5019) event, with the VM
and the CA certificate's SHA-256 fingerprint (SEC10-audit):

- issuing a CA;
- its install;
- the signing switch;
- its removal;
- an install `wb-guestd` refuses or doesn't report.

## Decisions

### No name constraints on the CA

- **Why:** the set of inspected hosts changes with every approval
  (S07-11-approvals-and-learning). A constraint that followed it would
  need a new CA each time. Clients that read trust once per process
  would then reject every leaf from the new CA until they restart.
- **Rejected:** a broad constraint that never changes. It limits
  nothing that leaf issuance doesn't already limit. RFC 5280 requires
  the extension to be marked critical, so a client that doesn't
  support it rejects the CA.
- **Decided:** I39 (B39-ca-rotation).

### Rotate the CA every 60 days, with an overlap

- **Why:** the hardware key can't be read out. But on every host,
  another process of the user can sign with it, or with a copy:
  - the wrapped key on macOS, which still signs on the same Mac after
    `wb-proxyd` removes the item;
  - the CNG key on Windows, which opens by its name;
  - the TPM blob or the software fallback key on Linux.

  Rotation bounds how long that is useful, now up to 60 days. Only
  that VM's guest trusts the CA. The thief also needs a position in
  that guest's traffic, which runs only through the host.
- **Overlap:** it keeps rotation from breaking running tools.
  Of the clients X04-tls-inspection tried, only Python's `requests`
  rereads its CA file while it runs. Go, Node, Python's `urllib`, Ruby
  and Java keep the trust they read at start. The clients it didn't
  test are assumed to do the same. So the overlap bounds their life,
  as "Overlap" says.

**Status:** Draft
