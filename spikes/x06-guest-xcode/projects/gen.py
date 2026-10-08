#!/usr/bin/env python3
"""X06-guest-xcode: write the test Xcode project X06Apps. Throwaway.

gen.py <dir>   writes <dir>/X06Apps.xcodeproj and the target folders.

Targets: MacApp (macOS SwiftUI app, App Sandbox, signed to run locally),
MacAppTests (unit tests hosted in MacApp), MacAppUITests, iOSApp,
iOSAppTests (hosted), iOSAppUITests. The project uses folder-synchronized
groups (objectVersion 77), so sources are not listed.
"""
import hashlib
import os
import sys

out = sys.argv[1]
objs = {}


def oid(name):
    return hashlib.md5(name.encode()).hexdigest()[:24].upper()


def add(name, d):
    i = oid(name)
    objs[i] = d
    return i


def q(s):
    return '"' + str(s).replace("\\", "\\\\").replace('"', '\\"') + '"'


def ser(v, ind=1):
    t = "\t" * ind
    if isinstance(v, dict):
        body = "".join("%s\t%s = %s;\n" % (t, q(k) if not k.isidentifier() else k, ser(x, ind + 1)) for k, x in v.items())
        return "{\n" + body + t + "}"
    if isinstance(v, list):
        body = "".join("%s\t%s,\n" % (t, ser(x, ind + 1)) for x in v)
        return "(\n" + body + t + ")"
    return q(v)


COMMON_DEBUG = {
    "ALWAYS_SEARCH_USER_PATHS": "NO",
    "CLANG_ENABLE_MODULES": "YES",
    "DEBUG_INFORMATION_FORMAT": "dwarf",
    "ENABLE_TESTABILITY": "YES",
    "ENABLE_USER_SCRIPT_SANDBOXING": "YES",
    "GCC_OPTIMIZATION_LEVEL": "0",
    "ONLY_ACTIVE_ARCH": "YES",
    "SWIFT_ACTIVE_COMPILATION_CONDITIONS": "DEBUG $(inherited)",
    "SWIFT_OPTIMIZATION_LEVEL": "-Onone",
    "SWIFT_VERSION": "5.0",
    "MACOSX_DEPLOYMENT_TARGET": "15.0",
    "IPHONEOS_DEPLOYMENT_TARGET": "18.0",
}

SIGN = {"CODE_SIGN_STYLE": "Automatic", "CODE_SIGN_IDENTITY": "-", "CURRENT_PROJECT_VERSION": "1", "MARKETING_VERSION": "1.0", "GENERATE_INFOPLIST_FILE": "YES", "PRODUCT_NAME": "$(TARGET_NAME)", "SWIFT_EMIT_LOC_STRINGS": "NO"}

TARGETS = [
    ("MacApp", "com.apple.product-type.application", "MacApp.app", {
        "SDKROOT": "macosx", "ENABLE_APP_SANDBOX": "YES", "ENABLE_USER_SELECTED_FILES": "readonly",
        "ENABLE_HARDENED_RUNTIME": "YES", "COMBINE_HIDPI_IMAGES": "YES",
        "LD_RUNPATH_SEARCH_PATHS": "$(inherited) @executable_path/../Frameworks",
        "PRODUCT_BUNDLE_IDENTIFIER": "org.wraithbox.x06.MacApp"}),
    ("MacAppTests", "com.apple.product-type.bundle.unit-test", "MacAppTests.xctest", {
        "SDKROOT": "macosx", "BUNDLE_LOADER": "$(TEST_HOST)",
        "TEST_HOST": "$(BUILT_PRODUCTS_DIR)/MacApp.app/Contents/MacOS/MacApp",
        "PRODUCT_BUNDLE_IDENTIFIER": "org.wraithbox.x06.MacAppTests"}),
    ("MacAppUITests", "com.apple.product-type.bundle.ui-testing", "MacAppUITests.xctest", {
        "SDKROOT": "macosx", "TEST_TARGET_NAME": "MacApp",
        "PRODUCT_BUNDLE_IDENTIFIER": "org.wraithbox.x06.MacAppUITests"}),
    ("iOSApp", "com.apple.product-type.application", "iOSApp.app", {
        "SDKROOT": "iphoneos", "TARGETED_DEVICE_FAMILY": "1,2",
        "INFOPLIST_KEY_UIApplicationSceneManifest_Generation": "YES",
        "INFOPLIST_KEY_UILaunchScreen_Generation": "YES",
        "LD_RUNPATH_SEARCH_PATHS": "$(inherited) @executable_path/Frameworks",
        "PRODUCT_BUNDLE_IDENTIFIER": "org.wraithbox.x06.iOSApp"}),
    ("iOSAppTests", "com.apple.product-type.bundle.unit-test", "iOSAppTests.xctest", {
        "SDKROOT": "iphoneos", "TARGETED_DEVICE_FAMILY": "1,2", "BUNDLE_LOADER": "$(TEST_HOST)",
        "TEST_HOST": "$(BUILT_PRODUCTS_DIR)/iOSApp.app/iOSApp",
        "PRODUCT_BUNDLE_IDENTIFIER": "org.wraithbox.x06.iOSAppTests"}),
    ("iOSAppUITests", "com.apple.product-type.bundle.ui-testing", "iOSAppUITests.xctest", {
        "SDKROOT": "iphoneos", "TARGETED_DEVICE_FAMILY": "1,2", "TEST_TARGET_NAME": "iOSApp",
        "PRODUCT_BUNDLE_IDENTIFIER": "org.wraithbox.x06.iOSAppUITests"}),
]

HOSTS = {"MacAppTests": "MacApp", "MacAppUITests": "MacApp", "iOSAppTests": "iOSApp", "iOSAppUITests": "iOSApp"}


def cfglist(name, debug, release):
    d = add(name + ".Debug", {"isa": "XCBuildConfiguration", "buildSettings": debug, "name": "Debug"})
    r = add(name + ".Release", {"isa": "XCBuildConfiguration", "buildSettings": release, "name": "Release"})
    return add(name + ".List", {"isa": "XCConfigurationList", "buildConfigurations": [d, r],
                                "defaultConfigurationIsVisible": "0", "defaultConfigurationName": "Release"})


products = []
syncs = []
targets = {}
for name, ptype, prod, settings in TARGETS:
    pref = add(name + ".product", {"isa": "PBXFileReference", "explicitFileType": "wrapper.application" if prod.endswith(".app") else "wrapper.cfbundle",
                                   "includeInIndex": "0", "path": prod, "sourceTree": "BUILT_PRODUCTS_DIR"})
    products.append(pref)
    sync = add(name + ".sync", {"isa": "PBXFileSystemSynchronizedRootGroup", "path": name, "sourceTree": "<group>"})
    syncs.append(sync)
    phases = [
        add(name + ".sources", {"isa": "PBXSourcesBuildPhase", "buildActionMask": "2147483647", "files": [], "runOnlyForDeploymentPostprocessing": "0"}),
        add(name + ".frameworks", {"isa": "PBXFrameworksBuildPhase", "buildActionMask": "2147483647", "files": [], "runOnlyForDeploymentPostprocessing": "0"}),
        add(name + ".resources", {"isa": "PBXResourcesBuildPhase", "buildActionMask": "2147483647", "files": [], "runOnlyForDeploymentPostprocessing": "0"}),
    ]
    s = dict(SIGN)
    s.update(settings)
    deps = []
    if name in HOSTS:
        host = HOSTS[name]
        proxy = add(name + ".proxy", {"isa": "PBXContainerItemProxy", "containerPortal": oid("project"), "proxyType": "1",
                                      "remoteGlobalIDString": oid(host + ".target"), "remoteInfo": host})
        deps.append(add(name + ".dep", {"isa": "PBXTargetDependency", "target": oid(host + ".target"), "targetProxy": proxy}))
    targets[name] = add(name + ".target", {
        "isa": "PBXNativeTarget", "buildConfigurationList": cfglist(name, s, s), "buildPhases": phases, "buildRules": [],
        "dependencies": deps, "fileSystemSynchronizedGroups": [sync], "name": name, "packageProductDependencies": [],
        "productName": name, "productReference": pref, "productType": ptype})

pgroup = add("products", {"isa": "PBXGroup", "children": products, "name": "Products", "sourceTree": "<group>"})
main = add("main", {"isa": "PBXGroup", "children": syncs + [pgroup], "sourceTree": "<group>"})
release = dict(COMMON_DEBUG)
release.update({"DEBUG_INFORMATION_FORMAT": "dwarf-with-dsym", "ENABLE_TESTABILITY": "NO", "GCC_OPTIMIZATION_LEVEL": "s",
                "ONLY_ACTIVE_ARCH": "NO", "SWIFT_ACTIVE_COMPILATION_CONDITIONS": "$(inherited)", "SWIFT_OPTIMIZATION_LEVEL": "-O",
                "SWIFT_COMPILATION_MODE": "wholemodule"})
objs[oid("project")] = {
    "isa": "PBXProject",
    "attributes": {"BuildIndependentTargetsInParallel": "1", "LastSwiftUpdateCheck": "2700", "LastUpgradeCheck": "2700",
                   "TargetAttributes": {targets[t]: {"TestTargetID": targets[h]} for t, h in HOSTS.items()}},
    "buildConfigurationList": cfglist("project", COMMON_DEBUG, release),
    "developmentRegion": "en", "hasScannedForEncodings": "0", "knownRegions": ["en", "Base"], "mainGroup": main,
    "minimizedProjectReferenceProxies": "1", "preferredProjectObjectVersion": "77", "productRefGroup": pgroup,
    "projectDirPath": "", "projectRoot": "", "targets": [targets[n] for n, *_ in TARGETS]}

proj = os.path.join(out, "X06Apps.xcodeproj")
os.makedirs(os.path.join(proj, "xcshareddata", "xcschemes"), exist_ok=True)
with open(os.path.join(proj, "project.pbxproj"), "w") as f:
    f.write("// !$*UTF8*$!\n")
    f.write(ser({"archiveVersion": "1", "classes": {}, "objectVersion": "77", "objects": objs, "rootObject": oid("project")}, 0))
    f.write("\n")


def ref(name, prod):
    return ('<BuildableReference BuildableIdentifier="primary" BlueprintIdentifier="%s" BuildableName="%s" '
            'BlueprintName="%s" ReferencedContainer="container:X06Apps.xcodeproj"/>' % (targets[name], prod, name))


PROD = {n: p for n, _, p, _ in TARGETS}
for scheme, app, tests in [("MacApp", "MacApp", ["MacAppTests"]), ("MacAppUI", "MacApp", ["MacAppUITests"]),
                           ("iOSApp", "iOSApp", ["iOSAppTests"]), ("iOSAppUI", "iOSApp", ["iOSAppUITests"])]:
    testables = "".join('<TestableReference skipped="NO">%s</TestableReference>' % ref(t, PROD[t]) for t in tests)
    xml = ('<?xml version="1.0" encoding="UTF-8"?>\n<Scheme LastUpgradeVersion="2700" version="1.7">'
           '<BuildAction parallelizeBuildables="YES" buildImplicitDependencies="YES"><BuildActionEntries>'
           '<BuildActionEntry buildForTesting="YES" buildForRunning="YES" buildForProfiling="YES" buildForArchiving="YES" buildForAnalyzing="YES">'
           + ref(app, PROD[app]) + '</BuildActionEntry></BuildActionEntries></BuildAction>'
           '<TestAction buildConfiguration="Debug" selectedDebuggerIdentifier="Xcode.DebuggerFoundation.Debugger.LLDB" '
           'selectedLauncherIdentifier="Xcode.DebuggerFoundation.Launcher.LLDB" shouldUseLaunchSchemeArgsEnv="YES">'
           '<Testables>' + testables + '</Testables></TestAction>'
           '<LaunchAction buildConfiguration="Debug" selectedDebuggerIdentifier="Xcode.DebuggerFoundation.Debugger.LLDB" '
           'selectedLauncherIdentifier="Xcode.DebuggerFoundation.Launcher.LLDB" launchStyle="0" useCustomWorkingDirectory="NO" '
           'ignoresPersistentStateOnLaunch="NO" debugDocumentVersioning="YES" debugServiceExtension="internal" allowLocationSimulation="YES">'
           '<BuildableProductRunnable runnableDebuggingMode="0">' + ref(app, PROD[app]) + '</BuildableProductRunnable></LaunchAction>'
           '</Scheme>\n')
    with open(os.path.join(proj, "xcshareddata", "xcschemes", scheme + ".xcscheme"), "w") as f:
        f.write(xml)

SRC = {
    "MacApp/MacApp.swift": '''import SwiftUI

enum Greeting {
    static let text = "hello from x06"
}

@main
struct MacApp: App {
    var body: some Scene {
        WindowGroup { Text(Greeting.text) }
    }
}
''',
    "MacAppTests/MacAppTests.swift": '''import AppKit
import XCTest
@testable import MacApp

final class MacAppTests: XCTestCase {
    func testGreeting() {
        XCTAssertEqual(Greeting.text, "hello from x06")
    }

    func testHostedInApp() {
        XCTAssertTrue(Bundle.main.bundlePath.hasSuffix("MacApp.app"), Bundle.main.bundlePath)
        XCTAssertNotNil(NSApplication.shared)
    }
}
''',
    "MacAppUITests/MacAppUITests.swift": '''import XCTest

final class MacAppUITests: XCTestCase {
    @MainActor
    func testLaunch() {
        let app = XCUIApplication()
        app.launch()
        XCTAssertTrue(app.staticTexts["hello from x06"].waitForExistence(timeout: 60))
    }
}
''',
    "iOSApp/iOSApp.swift": '''import SwiftUI

enum Greeting {
    static let text = "hello from x06"
}

@main
struct iOSApp: App {
    var body: some Scene {
        WindowGroup { Text(Greeting.text) }
    }
}
''',
    "iOSAppTests/iOSAppTests.swift": '''import UIKit
import XCTest
@testable import iOSApp

final class iOSAppTests: XCTestCase {
    func testGreeting() {
        XCTAssertEqual(Greeting.text, "hello from x06")
    }

    func testHostedInApp() {
        XCTAssertTrue(Bundle.main.bundlePath.hasSuffix("iOSApp.app"), Bundle.main.bundlePath)
    }
}
''',
    "iOSAppUITests/iOSAppUITests.swift": '''import XCTest

final class iOSAppUITests: XCTestCase {
    @MainActor
    func testLaunch() {
        let app = XCUIApplication()
        app.launch()
        XCTAssertTrue(app.staticTexts["hello from x06"].waitForExistence(timeout: 60))
    }
}
''',
}
for path, text in SRC.items():
    full = os.path.join(out, path)
    os.makedirs(os.path.dirname(full), exist_ok=True)
    with open(full, "w") as f:
        f.write(text)
