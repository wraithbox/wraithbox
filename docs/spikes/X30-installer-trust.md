# X30 - Installer refusal of a changed restore image

**Purpose:** The result of the spike for I262. Does the Virtualization
framework's macOS installer, run by `wb-vmd-install`, refuse a restore
image with a changed or replaced component, and does the install
contact Apple while `wb-vmd-install`'s profile denies the network?

**Requirements:** SEC12-least-privilege, SEC08-proj-isolation

**Brief:** B262-installer-trust

## For review

- **Decides:** what the trust base of S17-04-restore-and-release, "The
  trust base", rests on, now that the spike measured it.
- **You are approving:** the answer and these conditions:
  - the check of the boot components (kernel cache, boot loaders,
    ramdisk, device tree, trust caches) is Apple's signing server,
    which the installer asks over the network for a ticket. The
    installer hashes each component from the restore image itself, and
    the server declines a digest that isn't Apple's. A matching change
    to the image's `BuildManifest.plist` doesn't help;
  - the check of the system volume image is the restore OS inside the
    VM, which stops the install part way through the copy;
  - the install contacts Apple's signing server on every run. The
    request leaves from Apple's installation service, a host process
    that `wb-vmd-install`'s profile doesn't confine, not from
    `wb-vmd-install`. An image build needs the host's own
    network, outside `wb-download`'s compiled-in hosts.
- **Controls touched:** SEC12-least-privilege (least privilege on the
  host): `wb-vmd-install` keeps its profile, which denies it the
  network. The network use is Apple's service's, which the profile
  can't reach, and which the spec now names. SEC08-proj-isolation
  (per-project isolation): a guest OS built from a changed image
  doesn't install, so the separation of project users rests on
  Apple's OS.
- **Assumed:**
  - that an install fails when the signing server can't be reached.
    The spike didn't cut the host's network (agents stay off the
    host's settings). The log shows that the installer tries up to
    three times on `TSS_ERR_SERVER_NOT_REACHABLE`, and a declined
    request failed the install;
  - that the restore OS refuses the system volume image because the
    archive's authentication fails. The guest's own log isn't on the
    host. The host log shows only that the guest dropped the stream
    and reported `failed to restore APFS image`;
  - that the server keeps signing an older build for VMs for some time
    after a newer one ships. A compromised `wb-download` could then
    serve that older build (Not covered, I618).
- **Open decisions:** none. The spec changes below record the
  measured behavior. They don't weaken a control.

## Question

From I262: on a macOS 27 host, does `VZMacOSInstaller` refuse a restore
image with one byte changed in a signed component, or with a component
replaced, and at which step and with what error? Does the install
contact Apple (personalization) while `wb-vmd-install`'s profile denies
the network?

## Answer

**Yes, with conditions.** Every changed image failed to install, and
the unchanged image installed under the same profile:

| Case | Change | Result | Step | Error |
|---|---|---|---|---|
| c00-original | none, Apple's 27.2 (26B123) image | installed, 446 s | | |
| c02-kernel-flip | one byte of the kernel cache | refused after 9.4 s | personalization | `VZErrorDomain` 10007, under it `MobileRestore` 3194 "Declined to authorize this DFU file on this device for this user", server error 94 "This device isn't eligible for the requested build" |
| c06-kernel-flip-manifest | the same byte, and the kernel digests in `BuildManifest.plist` rewritten to match | refused after 9.7 s | personalization | the same as c02 |
| c03-os-flip | one byte in the middle of the system volume image (`141-12228-170.dmg.aea`, 11 GB) | refused after 219 s, at 48 % | copying the system volume | `VZErrorDomain` 10007, under it `MobileRestore` -1 "AMRestorePerformRestoreModeRestoreWithError failed with error: 1106", which the restore OS reports as `failed to restore APFS image` |
| c04-kernel-swap | the kernel cache replaced with the 27.2 kernel cache of a physical Mac (`kernelcache.release.mac17g`), which Apple signed | refused after 12.5 s | personalization | the same as c02 |
| c01-rezip | none, every entry stored again by the same tool (a control for the cases above) | installed, 143 s | | |

The refusal isn't a local check of the restore image. Apple's signing
server and the restore OS in the VM make it:

- **Apple's signing server, for the boot components.** The installer
  hashes each component it personalizes from the file in the image,
  and sends the digests to Apple's signing server (TSS) for a ticket
  bound to the VM's identity. In c02 the digest it sent for
  `KernelCache` was the SHA-384 of the changed file (`eb5e9cb7…`), not
  the manifest's (`1f686b08…`). The server declined it. Rewriting the
  manifest (c06) sent the same digest and got the same answer. A
  kernel cache that Apple did sign, for a physical Mac of the same
  build, was declined as well (c04): the server signs a digest only
  for the device and build it belongs to.
- **The restore OS in the VM, for the system volume.** The installer
  doesn't send a digest of the system volume image
  (`Personalize OS image = No` in the log,
  [`quoted-lines.txt`](https://github.com/wraithbox/wraithbox/tree/ddc9d33177d34209b5c4d1c2ed17d777a608ed9b/spikes/x30-installer-trust/results/quoted-lines.txt)). It sends the volume's root
  hash from the manifest. The restore OS boots from the personalized
  components, which the server has checked. It receives the image, and
  stopped the copy at the changed byte (c03).

The install contacts Apple on every run. In c00, Apple's installation
service (`com.apple.Virtualization.Installation`) made five TSS
requests and two other HTTPS requests to Apple. Every connection the
log shows it open went over the host's network: its 35 `path:satisfied`
events all name `en0` ([`quoted-lines.txt`](https://github.com/wraithbox/wraithbox/tree/ddc9d33177d34209b5c4d1c2ed17d777a608ed9b/spikes/x30-installer-trust/results/quoted-lines.txt)). `wb-vmd-install`'s profile denied `x30` the network
the whole time (its probe of `connect` to `127.0.0.1` got
`Operation not permitted`). The service is a separate process, an XPC
service of the framework, and the profile of the process that calls it
doesn't apply to it. Each TSS request holds the VM's chip identity
(`ECID`) and the build's component digests.

## Measurements

The host is the dedicated Mac, macOS 27.2 (26B123), and the restore
image is Apple's `UniversalMac_27.2_26B123_Restore.ipsw`. `x30` is the
X25-vmd-sandbox install code, ad-hoc signed with the virtualization
entitlement, and confined with that spike's `wb-vmd` profile plus the
three install rules of S04-04-vm-processes ("`wb-vmd-install`"). The
install uses 8 GiB of memory.

For each case, `mkipsw.py` writes a copy of the restore image with
every entry stored, in the original order. It changes the entries
that the case lists in `results/<case>.changes.jsonl` and copies the
rest. `run-case.sh` then runs one install, and records
`x30`'s result lines, the unified log of the Virtualization processes
(`log stream --level info`), and the host's flows once a second
(`nettop`). `summarize.sh` keeps the lines of the log that the result
rests on in `results/<case>.summary.txt`, and
`results/quoted-lines.txt` has the other log lines this page quotes. The full logs (1 MB to
6 MB each) stay out of the branch.

c01 installed in a third of c00's time. The spike didn't look into why.
Both installs completed, which is what the control is for.

| Case | Install start | `install-failed` or `installed` after | Progress at the end |
|---|---|---|---|
| c00-original | 20:44:14Z | 446 s | 100 % |
| c02-kernel-flip | 20:51:42Z | 9.4 s | 0 % |
| c06-kernel-flip-manifest | 20:51:53Z | 9.7 s | 0 % |
| c03-os-flip | 20:52:05Z | 219 s | 48 % |
| c04-kernel-swap | 20:56:39Z | 12.5 s | 0 % |
| c01-rezip | 20:56:54Z | 143 s | 100 % |

The log has every request, with its HTTP status. `nettop`'s one-second
samples caught only some of the service's flows: three, to three Apple
addresses on port 443, all on `en0` (`c00-original.summary.txt`, "host
flows").

## Not covered

- **An unreachable signing server.** Cutting the host's network needs
  the host's settings, which agents don't change. Assumed to fail the
  install (For review).
- **An older build that Apple still signs.** A compromised
  `wb-download` can serve any build that the signing server still
  accepts, an older one with known flaws among them. The SHA-1 check
  doesn't stop that, and this spike didn't measure which builds the
  server signs for a VM. I618 follows it up.
- **Booting a changed image installed some other way.** No changed
  image got past the installer, so the spike didn't try it.

## What it means for the specs

- S17-04-restore-and-release, "The trust base": the installer's check
  is measured. The boot components are checked by Apple's signing
  server, and the system volume by the restore OS. The install needs
  that server, which Apple's installation service calls over the
  host's network, outside `wb-vmd-install`'s profile. An older build
  that Apple still signs isn't caught.
- S04-04-vm-processes, "`wb-vmd-install`": the bullet for the installation
  service says that it calls Apple.

## Spike code

Throwaway code on branch `spike/x30-installer-trust`, commit
[`ddc9d33`](https://github.com/wraithbox/wraithbox/tree/ddc9d33177d34209b5c4d1c2ed17d777a608ed9b/spikes/x30-installer-trust):
`x30` (`Sources/x30/main.swift`, from X25-vmd-sandbox), the install
profile (`profiles/vmd-install.sb`), `mkipsw.py` and `manifest.py`,
which write the changed images, and `run-case.sh`, `run-all.sh` and
`summarize.sh`, which run and record each case.

**Status:** Answered 2026-11-08: yes, with conditions. The signing server and the restore OS refuse a changed image, and the install contacts Apple from a service the profile doesn't confine
