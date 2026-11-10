# S04-11 - Starting `wb-vmd`

**Purpose:** What `wb-vmd` inherits from its spawner, the socket it
serves gRPC on, its lifeline, and the checks on each connection. How it
confines itself is in S04-03-self-sandboxing.

## Design

### How `wb-vmd` is started

`wb-vmd` takes no arguments, and refuses any with `vmd-arguments`. It
inherits four descriptors, and in the `build` slot a fifth:

| Descriptor | What it is |
|---|---|
| 0 | the lifeline: a pipe or socket from its spawner that it only reads |
| 2 | the log pipe (S04-03-self-sandboxing, "Daemon logs") |
| 3 | a listening `AF_UNIX` stream socket for gRPC |
| 4 | the hand-off socket: `AF_UNIX`, `SOCK_DGRAM` (S04-02-vm-provider, "Hand-off on macOS") |
| 5 | `build` slot only: the read end of the build password pipe, which `wb-guestadmin-build` writes (S15-04-guest-admin, "Build") |

Descriptor 5 is the one exception of S12-01-platform-interfaces,
"Rules" (S04-10-vmd-install, "The build password pipe").

Its profile allows no path outside its bundle, so it can't bind a
socket. The spawner binds it in `<data>/run/` (S04-architecture,
"Inter-process contracts") and passes the listening descriptor. The
spawner is `wb-hostd` through `wb-launcher`, or a test. `wb-hostd`
names the socket `vmd-<slot>-<generation>.sock`, so no two runs share
a path, and removes it when the run ends.

The socket follows these endpoint rules of S04-architecture,
"Inter-process contracts":

- The socket is in the run directory, which `wb-hostd` makes and
  checks before it serves, and which the dialer checks before each
  connect.
- The socket file has mode `0600` after the bind.
- Each end checks the other's user ID: `wb-hostd` dials through
  `localipc`, and `wb-vmd` checks each peer it accepts.

The socket has no `.lock` of its own. `wb-hostd` holds the lock of its
own endpoint for its life, so one `wb-hostd` at a time makes and
removes `vmd-*.sock` files. When it stops, it keeps the lock after it
has closed its own socket, until every `wb-vmd` it spawned has ended.
Before the bind, `wb-hostd` refuses any file already at the path, a
symbolic link included, with `ipc-endpoint-unsafe` and leaves it,
because `bind(2)` follows a dangling link.

When `wb-hostd` crashes, its sockets stay. The next `wb-hostd` removes
every `vmd-*.sock` that is a socket once it holds its endpoint's lock,
before it builds the VM manager. It doesn't probe them with a connect,
because the `wb-vmd` processes of the crashed `wb-hostd` exited
when their lifelines closed. A file of that name that isn't a socket is
left and logged with `ipc-endpoint-unsafe`.

Right after the deny probe (S04-03-self-sandboxing, "Confined first
thing in `main`"), `wb-vmd` checks each descriptor: open, and
of the kind in the table. Descriptors 0 and 2 must be a pipe or a
socket, and descriptor 0 must block. Descriptor 3 must have a path of
its own and no peer. Anything else is refused with `vmd-descriptor-invalid`. So it
never serves on a descriptor its spawner didn't mean.

- **The lifeline.** End of file or an error on descriptor 0 means its
  spawner is gone. `wb-vmd` then stops its VM at once, logs
  `lifeline-closed`, and exits 0. That is its control channel of
  S04-07-supervision, "When `wb-hostd` stops", so a VM never outlives
  the process that asked for it.
- **Each connection.** On every accepted connection, `wb-vmd` first
  reads the peer's user ID with `LOCAL_PEERCRED`. A peer of another
  user ID, or one whose credential can't be read, is closed with
  `ipc-peer-uid` before a byte is read or written. Then comes the
  version exchange of S04-08-records-and-bounds, "Between host
  processes". gRPC reads nothing until both pass, and the peer gets
  nothing but the version record until then.

**Status:** Draft
