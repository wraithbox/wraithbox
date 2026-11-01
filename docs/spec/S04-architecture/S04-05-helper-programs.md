# S04-05 - Helper Programs

**Purpose:** The programs that run once for an image build or a store operation:
`wb-guestadmin`, `wb-release-check`, `wb-download` and `wb-build-ssh`.

## Design

### The guest admin password has a process of its own

`wb-guestadmin` does these (S15-least-privilege):

- the admin side of an image build;
- rotating `wbadmin`'s password at each VM's first contact;
- removing the items of images and system disks that are gone,
  including the old item when a VM's system disk is replaced.

It may touch the `wbadmin/*` items in the secret store and nothing
else. `wb-hostd` starts it through `wb-launcher` for one run, and it
exits when that run ends.

### `wb-guestadmin`'s channels

Its channels are:

- pipes to the build's `wb-vmd` and `wb-build-ssh`, whose read ends
  `wb-launcher` hands over at spawn;
- one host-guest connection to `wb-guestd`, which `wb-hostd` has
  `wb-vmd` open for it.

It only writes to that connection, so it parses nothing from the guest.
Before it writes, it checks with `LOCAL_PEERPID` that the connection's
peer is Virtualization's service process. It refuses any other
descriptor.

It doesn't talk to `wb-proxyd`. Its requests carry only item names, in
the fixed format of S15-03-access-rules. `wb-launcher` and
`wb-guestadmin` both check that format.

A compromised `wb-hostd` can still keep a read end of a build pipe, and
so read that build's password, because a pipe has no peer to check.
That password is burned at the build VM's first contact, and the
residual stays limited to builds.

### `wb-guestadmin`'s profile

`wb-guestadmin` confines itself at start the same way, from a
compiled-in profile (X23-sandboxed-daemons, X25-vmd-sandbox).

- The profile allows the secret store service for its `wbadmin/*`
  items, and its inherited descriptors.
- It denies files, the network, and starting programs.

`wb-launcher` has one fixed entry per run kind. A request then never
chooses what it does. The entries are:

- `wb-guestadmin-build`;
- `wb-guestadmin-rotate`;
- `wb-guestadmin-cleanup`, which removes the items of builds, images and
  system disks that are gone (S15-03-access-rules, rule 8).

### `wb-release-check`

`wb-release-check` checks a Claude Code release for an image build
(S14-01-install). `wb-launcher` has one fixed entry,
`wb-release-check`, with no profile parameter.

It confines itself first thing, from a compiled-in profile:

- The profile allows its inherited descriptors: the manifest, the
  signature, the binary and its result pipe.
- It denies files, the network, the secret store and starting
  programs.

Then it:

1. reads the requested version from the pipe;
2. runs the checks;
3. writes one fixed-format record to the pipe: `pass` with the
   manifest's signed SHA-256 checksum for the guest's platform, or the
   rule of the check that failed.

It exits after one check.

### `wb-download`

`wb-download` fetches what an image build needs
(S17-03-downloads). `wb-launcher` has two fixed
entries, `wb-download-restore` and `wb-download-claude`. Each has its
hosts compiled into the program. A request then never names a host or
a URL.

Its compiled-in profile is the network part of the `wb-proxyd` profile
that X23-sandboxed-daemons measured for I61, limited to TCP port 443.
It allows:

- name lookups;
- `com.apple.trustd.agent` and the metadata of the app bundle's
  directory, for certificate checks;
- the routing table `sysctl` read that `getifaddrs` makes, for the
  upstream address rule;
- its inherited descriptors.

It denies files, the secret store and starting programs.

`wb-launcher` sets each entry's file size limit (`RLIMIT_FSIZE`) before
it starts the program. So the host bounds what a run writes.

### `wb-build-ssh`

`wb-build-ssh` runs the image build's one SSH session
(S15-04-guest-admin, "The build's SSH session"). It is a fixed start of
`wb-launcher` like the others: no arguments, a fixed environment, and
only the descriptors `wb-hostd` hands it. It inherits:

- the read end of `wb-guestadmin`'s pipe, as its standard input;
- the connected socket to the build guest's port 22;
- its log pipe to `wb-hostd`.

The socket comes from `wb-netd`, which opens that one connection for
the build VM only (S07-18-build-network). The
host has no route to the guest, so no other process can open it.
`wb-netd` passes it to `wb-hostd` on its control channel, and
`wb-hostd` hands it to `wb-launcher` with the start request.

`wb-hostd` reads that message with the checks of
S07-05-stream-hand-off, "Message form", in the same receiver
(`fdchan`). It refuses the socket when it comes from another VM's
`wb-netd`, when no build waits for it, when the message doesn't hold
exactly one descriptor, or when the descriptor isn't a Unix domain
`SOCK_STREAM` socket. It then closes the message's descriptors and
logs `build-socket-refused`. It logs an accepted socket with
`build-socket-accepted`, the build and the VM generation.

`wb-build-ssh` speaks SSH in its own process, with Go's SSH library
(`golang.org/x/crypto/ssh`). It doesn't start a program. It doesn't
read the host user's SSH configuration, known-hosts file or keys.

It confines itself first thing, from a compiled-in profile:

- The profile allows its inherited descriptors.
- It denies files, the network, the secret store and starting
  programs.

Then it:

1. checks that the socket is a connected Unix domain `SOCK_STREAM`
   socket, and refuses to run with `build-ssh-socket-refused`
   otherwise;
2. reads one line, the build password, from its standard input;
3. logs in as `wbadmin` with that password, offering password
   authentication only, and not a key, an agent or keyboard-interactive;
4. starts one session, with no terminal, that runs the install
   commands compiled into it;
5. writes the password and a newline into that session's standard
   input, for the guest's `sudo -S`, and closes it, so the install
   commands run under one `sudo -S`;
6. exits with the session's result.

#### Deadlines

Go's SSH library doesn't set a deadline on a socket it didn't dial. So
`wb-build-ssh` sets its own, and each ends the run with its rule:

| Limit | Time | Rule |
|---|---|---|
| Handshake and login, from the start | 30 s | `build-ssh-login-timeout` |
| No byte from the guest during the session | 2 minutes | `build-ssh-idle-timeout` |
| The whole session | 15 minutes | `build-ssh-deadline` |

It reads at most 1 MiB of session output in total. More ends the run
with `build-ssh-output-limit`. The build fails on each of these
(S15-04-guest-admin, "The build's SSH session").

#### What reaches the log

Every byte it reads from the socket comes from the guest.

- **Session output.** `wb-build-ssh` replaces each copy of the
  password in the whole output stream, and carries a partial match at
  the end of one read into the next. Only then does it cut each line
  to a fixed length and write it to its log.
- **Other guest strings.** The server version, the disconnect text and
  the error text of the SSH library are escaped and cut as
  S07-02-packet-path, "Events and rate limits", says, and the password
  is replaced in them too.
- **The banner.** It is discarded and never logged.

`wb-build-ssh` never writes the password to its log, its arguments or
its environment.

## Decisions

### `wb-guestadmin` runs apart from `wb-hostd`

- **Why:** least privilege. `wb-hostd` runs the host-guest socket
  handlers, and so never reads a credential.

### Go's SSH library, not the system `ssh`

`wb-build-ssh` speaks SSH with `golang.org/x/crypto/ssh` in its own
process.

- **Why:** the connection exists only as the socket `wb-netd` hands
  over. OpenSSH closes inherited descriptors above standard error at
  start, and its proxy options would need another helper program. The
  library uses the socket as it is, reads none of the host user's SSH
  configuration, and doesn't need an askpass program.
- **Rejected:** the system `ssh` with a `wb-askpass` helper. It needed
  a second program in the bundle, an exception to the fixed starts of
  `wb-launcher`, and a two-line framing of the password on standard
  input.

**Status:** Draft
