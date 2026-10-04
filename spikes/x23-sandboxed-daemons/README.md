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
