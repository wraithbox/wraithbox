# Interfaces: specified vs implemented vs tested (6138336c)

| Category | Specified | Implemented | Tested |
|---|---|---|---|
| CLI top-level commands (S05, internal/cli table) | 22 commands in table (claude ... help) | 6 run: version, help, setup, vm, project, shell (rest print "not implemented yet", exit 2) | internal/cli tests the table and parser; setup, vmcmd, projectcmd have tests |
| CLI leaf commands incl. subcommands (S05-03 table, counted by hand) | 41 | 15: vm start/stop/suspend/status/reset-data (5 of 7; no reset-ca, admin), project show/confirm/move/rename/place/rm (6 of 6), shell, setup, help, version | all implemented ones have unit tests (suspend not verified as more than a refusal) |
| CLI global flags | --isolated, --ephemeral, --learn, --guest, -C, --help/-h, --version | all parsed in internal/cli; session flags refused on non-agent commands | yes (cli_test.go); the flags have no effect yet because `wb claude` is unimplemented |
| gRPC services | 5 services in 4 protos (HostService, ProjectService, ShellService, VMService, GuestService); 3 more protos are message-only (audit, diskfmt, terminal) | all 5 have Go servers/clients; VMService also Swift server | yes |
| gRPC methods | 26 rpc (4 Host, 8 Project, 1 Shell, 5 VM, 8 Guest); by method name in docs: VM 5 of 5, Guest 8 of 8, Shell 1 of 1, but 0 of 12 Host/Project methods (docs describe those in prose and the CLI table) | 26 of 26 have a Go method or call site; WatchState has no Go test hit | 25 of 26 have Go test refs |
| Proto messages (host-guest and host-vmd) | 101 messages in 8 protos; only 15 are named in docs (docs describe the exchange in prose) | 101 generated | 97 of 101 named in Go tests |
| netd link-filter rules (rule names, S07-02) | 19 Rule constants; 16 of 19 slugs appear in S07 | 19 | covered by link/stack tests and fuzz |
| Policy rule kinds (S09-01, S07-11): endpoint keys host, port(s), path, protocol, enforcement, access, rules, deny_rules; entry kinds pass hosts, raw TCP, audit hosts | 8 endpoint keys + 3 entry kinds + closed/refused key rules | none: no policy loader or matcher exists in Go (no internal/policy; wb-proxyd is a stub) | none |
