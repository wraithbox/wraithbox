# X23-sandboxed-daemons spike (throwaway)

Can `wb-netd`, `wb-proxyd` and `wb-hostd` confine themselves at start
with a Seatbelt profile while the Go runtime, inherited descriptors,
and the work they do keep working? Scope per issue #34: approach steps
1 and 2. The Keychain and Secure Enclave part of `wb-proxyd` is #61.

The answer is in `docs/spikes/X23-sandboxed-daemons.md` on `main`.

## Layout

- `sbxcgo/`: applies a profile with cgo (`sandbox_init_with_parameters`).
- `sbxpure/`: the same call without cgo: a libSystem trampoline as in
  `golang.org/x/sys/unix` (`cgo_import_dynamic`, `syscall.syscall6`).
- `probes/`: what a confined process can still do.
- `cmd/probe`: applies a profile, runs the probes.
- `cmd/netd`: a `wb-netd` stand-in: gVisor stack on an inherited
  datagram socket (fd 3), hands each guest TCP stream to a proxy over an
  inherited Unix socket (fd 4) with a fresh socketpair and SCM_RIGHTS.
- `cmd/harness`: guest stack, proxy and `wb-hostd` around one netd;
  measures echo throughput.
- `cmd/hostd`: a `wb-hostd` stand-in: state directories, Unix socket,
  a git round trip into a landing repository, and children started
  directly or through an unconfined spawner.
- `profiles/`: the profiles.

## Run (macOS, Apple Silicon)

```sh
./build.sh
./probe.sh probe-nocgo pure profiles/netd.sb       # in process, no cgo
./probe.sh probe cgo profiles/netd.sb              # in process, cgo
./probe.sh probe-nocgo exec profiles/exec-min.sb   # under sandbox-exec
./bin/harness -mech pure -netd bin/netd-nocgo -profile profiles/netd.sb
./bench.sh 5 64
./hostd.sh pure profiles/hostd.sb /opt/homebrew/bin/git /opt/homebrew
./hostd.sh pure profiles/hostd.sb /opt/homebrew/bin/git /opt/homebrew hostd-nocgo -spawner
./denials.sh 60 netd                               # kernel sandbox log
```

## wb-proxyd with Keychain and Secure Enclave (issue #61)

`i61/` holds a `wb-proxyd` stand-in that confines itself (the no-cgo
trampoline in `sbxpure`, inside a cgo binary) and then probes what
`wb-proxyd` needs: a `binding/` item, a wrapped Secure Enclave key whose
token object ID is kept in a `ca/` item, DNS and a TLS GET checked against
the system roots, and what must stay denied.

- `i61/build.sh` builds X29-dev-signing's Xcode project for its profile
  and puts the helper in `Contents/Helpers/i61.app`, signed by the
  personal team with the `binding` and `ca` access groups.
- `i61/run.sh <profile>` runs it under `i61/profiles/<profile>.sb` and
  appends the sandboxd violation reports (`results/`).
- `i61/minimize.sh <base> <rules>` drops each rule whose removal still
  lets the TLS probe pass. sandboxd reports only some denials, so this
  found the TLS rules, not the log.
- `i61/profiles/proxyd.sb` is the result. `i61/profiles/store-only.sb` is the
  store part alone, for `wb-credset` and `wb-guestadmin`.
