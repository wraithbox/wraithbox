#!/usr/bin/env python3
"""X06-guest-xcode: run one named task as the project user. Throwaway.

task.py <run label> <task> <how: setuid|asuser> [--nopty] [--sandbox GUESTPATH]

Sends a runas request through .scratch/g.sock (req.py) and appends the
answer to results/<run label>-tasks.jsonl.
"""
import os
import subprocess
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
SIM = "platform=iOS Simulator,name=x06-iphone"

XCB = {
    "mac-build": ("X06Apps", "-project X06Apps.xcodeproj -scheme MacApp -destination platform=macOS build"),
    "mac-bft": ("X06Apps", "-project X06Apps.xcodeproj -scheme MacApp -destination platform=macOS build-for-testing"),
    "mac-test": ("X06Apps", "-project X06Apps.xcodeproj -scheme MacApp -destination platform=macOS test"),
    "mac-uitest": ("X06Apps", "-project X06Apps.xcodeproj -scheme MacAppUI -destination platform=macOS test"),
    "ios-build": ("X06Apps", "-project X06Apps.xcodeproj -scheme iOSApp -destination generic/platform=iOS+Simulator build"),
    "ios-device-build": ("X06Apps", "-project X06Apps.xcodeproj -scheme iOSApp -destination generic/platform=iOS build CODE_SIGNING_ALLOWED=NO"),
    "ios-test": ("X06Apps", "-project X06Apps.xcodeproj -scheme iOSApp -destination " + SIM.replace(" ", "+") + " test"),
    "ios-uitest": ("X06Apps", "-project X06Apps.xcodeproj -scheme iOSAppUI -destination " + SIM.replace(" ", "+") + " test"),
    "pkg-xcb-test": ("X06Pkg", "-scheme X06Pkg -destination platform=macOS test"),
}
SCRIPTS = {"env", "spm", "sign", "sim", "xctest", "run-app"}

label, task, how = sys.argv[1:4]
rest = sys.argv[4:]
pty = "--nopty" not in rest
sandbox = rest[rest.index("--sandbox") + 1] if "--sandbox" in rest else None
name = "%s-%s-%s%s%s" % (task, how, "pty" if pty else "nopty", "-sb" if sandbox else "", "")
cmd = [sys.executable, os.path.join(HERE, "req.py"), ".scratch/g.sock", "runas", "--user", "x06p", "--how", how,
       "--timeout", "1800", "--log", os.path.join(HERE, "results", label + "-tasks.jsonl"), "--label", name]
if pty:
    cmd.append("--pty")
if sandbox:
    cmd += ["--sandbox", sandbox, "--work", "/Users/x06p/work"]
if task in XCB:
    d, args = XCB[task]
    # Spaces inside one argument are written as "+" above, and xcb.sh splits on spaces.
    cmd += ["--script", os.path.join(HERE, "guest", "tasks", "xcb.sh"),
            "--env", "X06_NAME=" + name, "--env", "X06_DIR=" + d, "--env", "X06_ARGS=" + args]
elif task in SCRIPTS:
    cmd += ["--script", os.path.join(HERE, "guest", "tasks", task + ".sh")]
else:
    sys.exit("unknown task " + task)
sys.exit(subprocess.call(cmd))
