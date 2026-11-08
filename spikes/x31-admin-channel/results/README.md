# X31-admin-channel results

macOS 27.2 (26B123) guest, bundle `i448/guest` (vm:install + vm:provision),
booted by `host-vmd` (`.scratch/x31/x31 run <bundle> ctl.sock 40 <mac> tools.cdr`)
with a vsock device and the tools disk 0.0.0-x31.2 attached read-only.
Installed at provisioning: wb-guestd 0.0.0-x31.1 (this checkout's code) and
x31-guestroot 0.0.0-x31.1 as `wb-guestroot`, two root LaunchDaemons
(KeepAlive), and the standard user `proj1`.

| File | Step | What it shows |
|---|---|---|
| install.log | provisioning | both daemons installed; no vsock device then, each exits ENODEV and launchd restarts it |
| race.json | 5 | 400 rounds of both update-at-start splits at once: no temp file left; "fresh" (dir missing): one of the two refuses `guestd-install-dir-failed` in 146/200 rounds; "existing": 200/200 both install |
| boot5.out | 5 | real boot with the new tools disk: guestroot installed itself at 12:44:09.788, wb-guestd itself at 12:44:09.970, no refusal, both at 0.0.0-x31.2 |
| s1-running*.out | 1, 4 | admin shell as root on /dev/ttys000 with wb-guestd running; step-4 tools |
| s1-sudo*.out | 4 | the same commands under sudo over SSH |
| tcc.out | 4 | SIP on; TCC has wb-guestroot denied (0), sshd-keygen-wrapper allowed (2) for kServiceSystemPolicyAllFiles |
| state-bootout.out | 2 | wb-guestd booted out: 1023 reset, 1022 shell works |
| state-sigstop.out | 2 | wb-guestd stopped (`Ts`): 1023 connects but sends nothing, 1022 shell works |
| state-crashloop.out, crash-poll*.jsonl | 2 | wb-guestd SIGKILLed whenever it runs (launchd throttle, 4 kills in 40 s): 1023 44/44 reset, 1022 44/44 banner, shell works |
| squat.out, squat2.out, squat-first-run.out | 3 | proj1 bind of 1022: EACCES on every try (2902, 2883, 6861 tries) during a restart and while booted out; root binds it; proj1 binds 5022; proj1 connect CID 1 EFAULT, CID 2 EAGAIN, CID 3 hangs |
| panic*.out, up2.out | 3 | proj1's hung connect to CID 3 was killed: guest kernel panic "corrupt list", panicked task x31-probe, twice (12:49:22, 12:51:35) |
| restart.out | 3 | SIGKILL of wb-guestroot: launchd's new one runs at the first 100 ms poll, 3/3 |
