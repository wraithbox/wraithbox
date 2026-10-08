# X06-guest-xcode spike (throwaway)

Throwaway code for X06-guest-xcode, never merged. The result is
`docs/spikes/X06-guest-xcode.md` on `main`.

Host: Apple M2, 16 GB, macOS 27.0.1 (26A434), Xcode 27.0 (27A266a), no
`sudo`. Guest: an APFS clone (`g1`) of X17-image-build's sealed bundle
`e5` (tag `spike-x17-image-build`), macOS 27.0.1, 4 vCPU, 8 GiB, file-handle
network with nothing behind it (one boot with a NAT network, only to
download the iOS simulator runtime), plus one read-only raw disk
`xcode.img` that holds a copy of the host's `/Applications/Xcode.app`,
the test projects, `x06-guestd` and the X27 safehouse profile.

## Parts

- `host-vmd/`: Swift `wb-vmd` stand-in. `x06 serve <bundle> <socket>`
  boots the bundle and bridges a host Unix socket to vsock port 1000.
- `go/cmd/x06-guestd/`: the guest daemon, swapped in for `x17-guestd`.
  Adds `runas`: a script as the project user, by `setuid` (credentials set
  on the child, new POSIX session, the way S06-vm-lifecycle's session
  helper does) or by `asuser` (`launchctl asuser <uid>` then drop to the
  user), with or without a PTY, optionally under `sandbox-exec -f`.
- `req.py` sends one request, `task.py` runs a named task as `x06p`,
  `sbloop.py` runs a task under the profile and prints denials,
  `matrix.py` is the final run, `bisect.py` and `bisect2.py` cut the
  safehouse profile to find the hang.
- `guest/`: root scripts (swap the daemon, install Xcode, create the
  project user, write the profiles, collect denials, kill the user's
  processes) and `tasks/` (what runs as the project user).
- `guest/xcode.sb`: the Xcode exceptions (profile p1).
  `guest/xcode-simtest.sb`: what `xcodebuild test` on a simulator needed
  in addition (p2).
- `projects/`: `gen.py` writes the Xcode project `X06Apps` (macOS app,
  iOS app, hosted unit tests and UI tests for both). `X06Pkg` is a SwiftPM
  package with a resource bundle.

## Results

- `final-matrix.txt`: every task in every mode on a fresh boot. The
  answers come from this file. `final-tasks.jsonl` has the full output.
- `g1-*`: first boot: Xcode install, project user, first runs, the
  testmanagerd experiments.
- `g2-*`: the NAT boot: iOS runtime download, first sandbox runs.
- `g3-*`: simulators, process and kill checks, the profile work.
  `g3-bisect-tasks.jsonl` is the hang bisection.
- `g3-sim-setuid.jpg`: the simulator screen with the app, taken by a
  project user with no GUI login.
