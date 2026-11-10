# S15-06 - The Binding Commands

**Purpose:** How `wb cred` sets, lists and removes the binding secrets
in the secret store through the `wb-credset` helper, while `wb` itself
holds no store access.

**Requirements:** SEC12-least-privilege, SEC04-no-guest-secrets

**Tests:** C05-credentials-and-store

## Design

### `wb-credset`

`wb-credset` is the helper that `wb cred set`, `wb cred rm` and `wb
cred list` run as the user.

- `wb` starts it from the installed bundle (S10-tech-stack).
- Its arguments are the operation and the binding name, never the
  value.
- It sets its core file limit to 0 (S13-01-process-hardening, "Limits
  and environment").
- Before it reads its arguments or standard input, it opens the store
  and then confines itself, from a compiled-in profile. The profile
  allows the secret store service and its standard streams. It denies
  files, the network and starting programs (S04-03-self-sandboxing,
  "Each host daemon is self-sandboxed").
- When it can't set the core file limit, it refuses with
  `credset-no-core`. When it can't confine itself, it refuses with the
  sandbox's rule, or `credset-not-confined` when the sandbox names
  none. Either way it exits 1 before it reads its arguments or
  standard input, and `wb` prints the rule.
- The open comes first because the profile blocks the read of the
  process's own signature, which the open needs (S15-02-secret-store,
  "The group of each call").

It does one operation and exits:

- *write* reads the value from its standard input;
- *delete* removes the item;
- *list* prints the names of the `binding/` items, never a value.

It takes exactly two arguments, the operation and the binding name.
Its environment has only the variable names on a fixed list compiled
into it, which `wb` also uses when it starts it. The list is empty.
Before any store call, it refuses each of these with the rule
`credset-refused`:

- any other argument count;
- any variable not on that list;
- an operation or name outside the fixed forms of
  S15-03-access-rules, "Declared access".

The operations are `write`, `delete` and `list`. The second argument
of `list` is `-`. A refusal names a bad argument by its length, and
never prints it.

A *write* reads at most 16 KiB from its standard input. It refuses an
empty value and a longer one with `credset-refused`, before any store
call. It zeroes its buffer when the call returns.

A failed store call is refused with the store's rule
(S15-02-secret-store, "Failed store calls"). For a locked store that
refusal comes at once, without a wait for the unlock. `wb` tells the
user to unlock the Mac's own screen and run the command again, since a
remote or SSH session doesn't unlock the Keychain. A *delete* of a
binding that isn't in the store succeeds, and its decision has the
rule `store-item-not-found`.

It logs one decision line, a JSON line on its standard error with the
message `wb-credset decision`. The line holds the decision, the rule,
the operation, the binding name and the store's status, never the
value. It exits 0 after the operation, and 1 after a refusal. `wb`
reads the outcome from that line, and prints the rule and its fix.

### `wb cred`

`wb cred` runs `wb-credset` by its path in the bundle `wb` runs from,
never from `PATH`. It checks the binding name first, and exits 2 on a
bad one before `set` reads anything. It refuses with `cred-no-bundle`
when its bundle's build version isn't its own, as `wb setup` does
(S17-02-setup, "The bundle check"), and when the helper's path goes
through a link.

`set` sets its core file limit to 0, then reads the value from a prompt
that doesn't show it. It refuses a prompt line of 1000 bytes or more,
which the terminal may have cut, and names a pipe instead. When standard
input isn't a terminal, it reads standard input to its end, and removes
one trailing newline, so `echo token | wb cred set github` stores
`token`. It refuses an empty value, or one over 16 KiB, before
`wb-credset` starts. It zeroes its buffer once the write to
`wb-credset` ends.

`list` prints the names on standard output, and the signing team on
standard error (S15-02-secret-store, "Backend guarantees"). `rm` of a binding that
isn't in the store succeeds, and says so on standard error only. Output
from `wb-credset` longer than `wb` keeps fails the run with
`cred-helper-failed`, and prints no names. A refusal ends with
`wraith box: <message> (<rule>)` after the fix, and exits 255
(S05-02-exit-status-and-signals). The rule is `wb-credset`'s, or the
store's, as it came.

`wb cred` doesn't write an audit record yet. It logs each access at warning
level, before and after `wb-credset` runs, with the operation and the
binding name (S15-03-access-rules, rule 6).

## Decisions

### An empty environment list

`wb-credset` doesn't read a variable, and `wb` starts it with an empty
environment.

- **Why:** the store doesn't need a variable. The host tests ran every
  Keychain call with an empty environment but for their own (I505). A
  name added to the list in `internal/credset` needs a comment that
  says what needs it.
- **Rejected:** passing the user's environment on. Each variable is
  input that the helper doesn't use.
- **Decided:** I504, decision 4, 2026-10-31.

### `-` as the binding of a list

`list` takes `-` as its second argument, so `wb-credset` always takes
exactly two arguments.

- **Why:** a list doesn't name a binding, and one argument count for
  every operation is one check.
- **Rejected:** a list with one argument. The count would depend on the
  operation.
- **Decided:** I504, decision 3, 2026-10-31.

### A locked store without a wait

`wb-credset` refuses at once with `store-locked`, and doesn't wait for
the unlock.

- **Why:** a helper that does one operation has no work to resume, and
  the user runs the command again after the unlock. On 2026-11-10 a
  `wb cred set` with the console screen locked, run through a remote
  session, got -25308 (C05-credentials-and-store, "wb-credset"), so
  the fix names the Mac's own screen.
- **Rejected:** a wait for the unlock, as S15-03-access-rules rule 7
  gives the work that resumes. Nothing in `wb cred` resumes.
- **Decided:** I504, decision 5, 2026-10-31.

### Open before confine

`wb-credset` opens the store, then confines itself.

- **Why:** on the maintainer's Mac on 2026-11-10, an open after the
  confinement got OSStatus 100001 and was refused with
  `keychain-needs-team`. The Keychain item calls still worked after
  it (`TestC05HostConfineFirst` and `TestC05HostConfineAfterOpen`,
  C05-credentials-and-store, "Host tests"). S04-03-self-sandboxing,
  "`wb-proxyd` and the secret store on macOS", gives the rule for every
  program with store access.
- **Rejected:** the confinement first, so that the open runs confined
  too. The open can't read the process's own signature then.
- **Decided:** I504, decision 2, 2026-10-31. I505 measured it on
  2026-11-10.

### The audit record comes later

The audit record of each `wb cred` access comes from `wb`, which asks
`wb-hostd` to write it before and after `wb-credset` runs, and refuses
when the first record can't be written. I514 builds it.

- **Why:** `wb-credset` reaches no file and no socket, and `wb` starts
  it, not `wb-hostd`. The record's writer isn't built yet.
- **Rejected:** no record, only `wb`'s log line. SEC10-audit would have
  a hole for credential changes.
- **Decided:** I504, maintainer decision 1 (option A), 2026-10-31.

## Out of scope

- Placeholders and bindings in the policy, and any check of a name
  against the declared bindings: M4.10.
- The expired-profile report on a `SIGKILL` of `wb-credset`: M6.6.

**Status:** Draft
