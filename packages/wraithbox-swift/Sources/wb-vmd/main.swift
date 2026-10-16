// wb-vmd: the macOS VM provider. One process runs one VM, in its slot's
// bundle, confined to that bundle, for wb-hostd. See
// docs/spec/S04-architecture/S04-03-self-sandboxing.md and
// docs/spec/S04-architecture/S04-04-vm-processes.md.
import WraithBoxVMD

VMDaemon.main()
