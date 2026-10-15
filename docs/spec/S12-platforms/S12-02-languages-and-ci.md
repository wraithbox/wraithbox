# S12-02 - Languages and CI

**Purpose:** The language used on each platform, and how CI builds each
platform.

## Design

### Languages per platform (NFR07-maintainability)

- **Go everywhere** for the core, the guest agent, and as much platform
  code as possible: HCS, Hyper-V sockets, and named pipes on Windows;
  KVM-based VMMs, D-Bus, Landlock, and seccomp on Linux.
- **macOS: Swift**, built with Xcode, for the Virtualization framework
  (`wb-vmd`) and notifications (native helper). The Keychain and Secure
  Enclave are reached from Go through a thin cgo shim, linked into each
  process that S15-least-privilege declares store access for, so no
  process gets a credential through another (SEC04-no-guest-secrets, SEC12-least-privilege).
- **Windows: C# on .NET**, built with the `dotnet` CLI, for a native
  notification and tray helper when that is built. Nothing else on
  Windows is expected to need it.
- **Linux: no native language expected.** If one is ever needed, it gets
  a spec change first.

### CI

Go is built, linted, vetted, and tested natively on macOS, Ubuntu, and
Windows runners from the start (S02-toolchain). Swift runs on macOS only. A
.NET job is added with the first .NET component.

**Status:** Draft
