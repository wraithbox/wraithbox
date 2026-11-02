# X32 agent D: specs, traceability, tests, structure (snapshot 6138336c, 2026-11-02)

Repo history: 796 commits since 2026-10-03. Go LOC counts non-blank, non-comment lines, default GOOS (darwin).

## 1. Docs
- Spec: 111 md files, ~120k words, 18 S-areas, ~1,210 H2+ sections. Largest: S07-egress-gateway (19 files, 22.0k words), S06 (19.5k), S04 (16.3k), S09 (12.8k). Other docs: 23 spike pages 65k words, 13 test pages 21.8k, 9 research notes 21.3k, agents 10.3k.
- Counts: FR 17, NFR 8, SEC 15, T 21, C test-case pages 12 (individual cases are unnumbered prose), X 31, R 8, B 46 briefs.
- Spikes: 22 answered, 9 open (X10-X16 other platforms/OpenShell/ESF, X30, X31). Decisions: 8 "Decided" mentions in 7 X00 rows (X01 twice, X02, X05, X08, X09, X29; other answers record conditions only), 8 V1 decisions (V1-01..V1-08), briefs: 43 Answered+Decided, 2 Answered only, 1 awaiting decision (B419-admin-path).
- Commits touching docs/spec per ISO week: W40 31, W41 142, W42 140, W43 36, W44 78 (total 427; W45 just started).

## 2. Traceability (ID referenced from non-test code / tests / any doc other than its index)
| Kind | Defined | In code | In tests | Either | Untraced |
|---|---|---|---|---|---|
| SEC | 15 | 12 | 11 | 12 (80%) | 3: SEC01, SEC06, SEC07 |
| NFR | 8 | 3 | 2 | 3 (38%) | 5 |
| FR | 17 | 2 | 0 | 2 (12%) | 15 (4 have no milestone: FR11, FR12, FR16, FR17; V1-08 puts FR11/FR12 out of V1) |
| T | 21 | 2 | 2 | 2 (10%) | 19 (threats are accepted risks, code rarely cites them) |
| C pages | 12 | 2 | 9 | 9 (75%) | 3 unreferenced from any test: C03-policy-and-approvals, C06-model-api, C07-dependency-gate (policy/model/dep-gate not built) |
Untraced requirements total: 23 of 40 FR/NFR/SEC (3 SEC, 5 NFR, 15 FR). Ten most security-relevant untraced: SEC01-separate-kernel, SEC06-repo-writes, SEC07-dep-gate (all 3 untraced SEC), FR09-approve-unknown, FR08-no-proxy-config, T03-hypervisor-escape, T04-bad-approvals, T05-cross-proj-clones, T06-forged-labels, T07-ungated-sources. SEC06/SEC07 are real gaps (git push control and dep gate unbuilt); SEC01 is implemented but never cited; FRs are cited by spec/milestones, not code.

## 3. Tests and coverage
- Go: 1,258 `Test*`, 43 `Fuzz*`, 0 `Benchmark*`; Swift 156 `@Test`. Go test LOC 45.1k vs code 35.9k (ratio 1.26; generated 8.2k excluded). Swift tests 3.3k vs sources 3.5k (0.93).
- Go statement coverage (`go test -cover`, per-package profile, offline, darwin, all pass): **82.3%** (13,122 of 15,950 stmts, generated excluded). Lowest: wb-diskfmt 28%, wb-dev-vm 35%, wb-dev-app 39%, platform/guestmount 44%; test-helper packages (*test, vmdfake) show 0% by design.
- Swift line coverage: **72.0%** of 3,268 lines (VM 98.8%, WraithBoxVMD 73.8%, wb-dev-bundle 58.4%). 156 tests passed, 3 skipped (needs-vm boot, macOS 27 gate).
- Never in CI (needs guest or attended host): 18 Go conformance tests (tag `conformance && darwin`, compiled but not run); needs-vm Go tests in wb-hostd (3: TestC09ShellVM, TestC09GuestUsersVM, TestC11HostdVM, run via `go:test-vm`); wb-diskfmt TestHostFormat (attended, env var); Swift "Boot a VM" suite (2). Go CI runs on macOS, Linux and Windows runners; 4 Go tests skipped here, 30 t.Skip sites in total, all conditional (root, case-sensitive volume).

## 4. Fuzzing
43 targets (41 real). Guest-input parsers on the host side (guestlink, guestconn replies, shell frames, kills, audit labels, logpipe, events, fd hand-off, terminal filter) all have targets; netd link layer yes. No target and no code: wb-proxyd HTTP/policy/dependency-gate parsers, git transport framing, pack scanner, pre-receive input, policy YAML (all specified in C03/C07/C08 Fuzzing sections). Swift wb-vmd inputs have no fuzzing. Only 1 corpus file in testdata, so it is smoke-level (10 s per target). Detail: fuzz_vs_guest_input.md.

## 5. Structure
- Go: 103 packages (97 non-generated), 35.9k code LOC. Biggest: hostd/hostservice 1,574, hostd/audit 1,363, termfilter 1,350, project 1,271, hostd/guestconn 1,179. Largest file: platform/fdchan/fdchan_darwin.go 399 (none near the 800 limit). Fan-in: version 20, vmbundle 10, platform/localipc 9. Fan-out: hostservice 19, cmd/wb-hostd 18.
- Binaries (transitive internal pkgs): wb-hostd 40 (17.7k LOC), wb 28 (11.4k), wb-guestd 21 (8.1k), wb-dev-app 13, wb-dev-vm 11, wb-diskfmt 10. **wb-netd, wb-proxyd, wb-credset, wb-guestadmin are 11-line stubs ("not implemented yet")**; `wb` prints the same for 16 of 22 commands.
- Complexity (non-test, non-generated, ~2,505 top-level funcs; limit 15): gocognit >1 on 1,147 funcs: 1-5 787, 6-10 315, 11-15 45, >15 0 (max 15, mean 4.67). gocyclo >1 on 1,638: 1-5 1,157, 6-10 450, 11-15 31, >15 0 (max 15). Roughly 870-1,350 funcs score 0 or 1 (not reported).
- Directives: 33 `//nolint`, all with a reason (14 usetesting for sun_path length, 13 gosec, 5 noctx, 2 revive; one line names noctx and gosec); 2 `swiftlint:disable all`, both in generated protobuf Swift.

## 6. Doc comments
Go exported identifiers: 2,107 of 2,157 (97.7%) documented (undocumented are mostly String/Error methods). Packages with a package comment: 96 of 96. Swift public decls with `///`: 130 of 198 (65.7%).

## 7. Dependencies
Go: 23 modules (10 direct, 13 indirect), libyears 4.25 total (direct only 0.24; google/btree 2.18, x/exp 1.26, x/time 0.52 are the lag). Swift: 22 resolved pins (5 direct). bun: 8 direct (4 deps, 4 dev), 619 lock entries. mise: 12 tools. Dependabot: 5 merged PRs (206 merged PRs overall), 0 open.

## 8. Interfaces (interfaces.md)
CLI: 22 top-level commands specified, 6 implemented; leaf commands 41 specified, 15 implemented, all tested. gRPC: 5 services, 26 methods, all implemented, 25 with Go tests; 101 proto messages. Policy rule kinds: spec 8 endpoint keys + 3 entry kinds, 0 implemented; netd link rules 19 implemented.

## Data files (data/d/)
docs_by_area.csv, docs_by_dir.csv, doc_counts.json, ids.json, spec_commits_per_week.csv, traceability.csv, traceability_summary.json, go_packages.csv, go_cmd_packages.csv, go_structure_summary.json, go_coverage_by_package.csv, go_coverage_total.json, go_cover.out, go_test.json, go_test_skips.txt, swift_llvm_cov_report.txt, swift_coverage_by_module.txt, swift_test.log, fuzz_targets.csv, fuzz_vs_guest_input.md, complexity_summary.json, complexity_functions.csv, complexity.json, golangci-complexity.yml, nolint.txt, nolint_by_linter.txt, go_doc_comments_by_pkg.csv, go_exported_undocumented.txt, swift_public_undocumented.txt, go_libyear.csv, dependabot_merged.json, grpc_methods.csv, proto_messages.csv, interfaces.md. Scripts: spikes/x32-project-metrics/d-structure/ in the worktree.

## Doubts
- ID matching is textual (`SEC08` or `SEC08-slug`); code citing a requirement in another way is missed. "T" and "C" short forms could false-match. FR/T low traced share reflects citing convention as much as missing work.
- Coverage is per-package own-test coverage (no -coverpkg), so cross-package tests do not count. Darwin only: linux/windows files unmeasured. Skips depend on this Mac (root, volume case).
- gocognit/gocyclo report only scores >1; the 0-1 band is estimated from a `^func` count that misses methods on multi-line signatures and closures. Needed `uniq-by-line: false` to get both linters' output.
- Swift public-declaration check is a regex heuristic; the library modules are mostly internal, so "public" may under-count.
- gRPC "implemented" counts any Go method or call site named like the rpc, so client wrappers inflate it. Spec CLI leaf count (41) was tallied by hand.
- Decision counts: X00 "Decided" wording is inconsistent with brief status (43 briefs say Decided), so decided spikes are likely higher than 7.
- Libyear for pseudo-versions compares timestamps of pseudo-versions; time source is proxy.golang.org.
- Only 5 dependabot merges: most dependency bumps may be done by hand or by agents.
