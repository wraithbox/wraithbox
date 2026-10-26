# X08 follow-up (I228): a host-made data volume in the guest

Throwaway spike code. Never merged. The result is appended to
`docs/spikes/X08-data-disk.md` on `main`.

Questions: does a guest ever turn on encryption for the data volume
that `wb-diskfmt` made on the host, does that volume give the space the
guest frees back to the host, and does the confined `diskutil` run work?

## Steps as run (2026-10-26, macOS 27.0.1 26A434 host and guests)

1. On the host, from the repository root at origin/main `3dd4c0c`:
   `(cd packages/wraithbox-go && WB_DISKFMT_HOST_TEST=1 go test -count=1 -run TestHostFormat -v ./cmd/wb-diskfmt)`.
   The confined shim made `data.wbdisk` (8 GiB, volume UUID
   `4A4B13A8-F535-4ECC-A777-C0DF42D6A076`) under `.scratch/`
   (`results/host-format.txt`).
2. `mise run conformance:build`, and `x8mount/` built with
   `GOWORK=off GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -o ../../../.scratch/x8mount .`.
   `x8mount` calls wb-guestd's own `guestd.DataMount` (Mount, then
   Check) without wb-hostd. Its module path sits under the product
   module's, so it may import `internal/`.
3. Three boots, each a fresh `mise run vm:clone dev/base i228/bN` and
   `mise run vm:provision i228/bN 90`. The clone's blank
   `data.wbdisk` was moved aside to `data.wbdisk.blank`, and the one
   host-made disk was moved in: from `.scratch/` into b1, then from b1
   to b2 and from b2 to b3. So b2 and b3 are system disk swaps.
4. In each guest: `boot-check.sh` (b2, b3) or the same steps by hand
   (b1): `guest-report.sh` (diskutil info and `apfs list`), `x8mount`
   as root, `guest-report.sh` again, and `datamount.test -test.run
   TestMounted` as the SSH user. In b1, all of `datamount.test` as the
   user and as root, `setuid-probe.sh` and `remount-probe.sh`.
5. Space: `space.sh` writes N MiB of random data in the guest, syncs,
   deletes it, and polls the image's `st_blocks` on the host.
   `bisect-mount.sh` remounts the volume with each option set first.
   `watch-alloc.sh` polls the image.
6. b1 and b3 ended with `halt.sh` (a shutdown in the guest), b2 with
   `mise run vm:stop i228/b2`, whose guest didn't shut down in the
   grace time and was stopped.

## Files

- `gssh.sh`, `groot.sh`: SSH and sudo into a provisioned test guest.
- `guest-report.sh`, `boot-check.sh`, `x8mount/`: the encryption
  report and wb-guestd's mount.
- `space.sh`, `bisect-mount.sh`, `remount.sh`, `watch-alloc.sh`: space
  given back to the host, by mount option.
- `setuid-probe.sh`, `remount-probe.sh`: why two datamount
  conformance checks fail.
- `results/`: raw output of every run.
