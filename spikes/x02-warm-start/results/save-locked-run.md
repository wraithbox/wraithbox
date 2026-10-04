# X02-warm-start: save while the host screen was locked

This file was put together after the run. The first `x02 cycle base 20 60`
run (started 17:11:56 CEST) wrote its stderr to `results/cycle-4g.err`
and its stdout to `results/cycle-4g.jsonl`. The second run, after the
unlock, overwrote both files. The "Tool output" section holds the first
run's lines exactly as the agent session showed them. The other sections
come from the macOS unified log and from `ioreg`, read during the session.

## Tool output, first run (transcribed from the session, verbatim)

stderr (tail):

    [2026-10-04T15:11:56Z] settling 60.0s
    [2026-10-04T15:13:00Z] error: Error Domain=VZErrorDomain Code=11 "The virtual machine failed to save with error “permission denied”." UserInfo={NSLocalizedFailure=An error occurred while saving the virtual machine., NSLocalizedFailureReason=The virtual machine failed to save with error “permission denied”.}

stdout (last line):

    {"error":"Error Domain=VZErrorDomain Code=11 \"The virtual machine failed to save with error “permission denied”.\" UserInfo={NSLocalizedFailure=An error occurred while saving the virtual machine., NSLocalizedFailureReason=The virtual machine failed to save with error “permission denied”.}"}

## Host lock state at 17:14 CEST (ioreg -n Root -d1, excerpt)

    IOConsoleLocked = true
    "CGSSessionScreenIsLocked"=Yes, "CGSSessionScreenLockedTime"=1791121094
    (date -r 1791121094: Sun Oct  4 15:38:14 CEST 2026)

## Secure Enclave key refused at the save (unified log)

See `save-locked.log` (17:13:00, `ctkd` and `SecKeyCreateRandomKey`).

## Unlock at 17:30:15 CEST (unified log, process loginwindow, filtered)

loginwindow logs "Keybag is locked, setting promptForPassword = YES"
before the unlock completes.

    2026-10-04 17:30:14.975 Df loginwindow[627:f9ee59] [com.apple.wallpaper:framework] Take Assertion 289: locked
    2026-10-04 17:30:14.988 Df loginwindow[627:f9f6b2] [com.apple.loginwindow.logging:Standard] -[LWScreenLock startUnlock:] | entered newValue: kLWUnlockFromUserActive (9), oldValue: kLWLockFromDisplayDim (5), isMainThread: 0
    2026-10-04 17:30:14.988 Df loginwindow[627:f9f6b2] [com.apple.loginwindow.logging:Standard] -[LWKeybagSupport nullKeybagUnlockForScreenLock] | enter
    2026-10-04 17:30:14.989 Df loginwindow[627:10ed] [com.apple.loginwindow.logging:Standard] -[LWScreenLock startUnlock:]_block_invoke | called CGSForceShowCursor from start unlock
    2026-10-04 17:30:15.008 Df loginwindow[627:f9f6b2] [com.apple.loginwindow.logging:Standard] -[LWKeybagSupport nullKeybagUnlockForScreenLock] |   MKBUnlockDevice returned: -3, so will return NO
    2026-10-04 17:30:15.008 Df loginwindow[627:f9f6b2] [com.apple.loginwindow.logging:Standard] -[LWKeybagSupport nullKeybagUnlockForScreenLock] |   returning: 0
    2026-10-04 17:30:15.008 Df loginwindow[627:f9f6b2] [com.apple.loginwindow.logging:Standard] -[LWScreenLock unlockFromEFI] | enter
    2026-10-04 17:30:15.008 Df loginwindow[627:f9f6b2] [com.apple.loginwindow.logging:Standard] -[LWScreenLock startUnlock:] |      Keybag is locked, setting promptForPassword = YES
    2026-10-04 17:30:15.008 Df loginwindow[627:f9f6b2] [com.apple.loginwindow.logging:Standard] -[LWScreenLock startUnlock:] |      _lockUIPresentedWithoutSuccessfulUnlock = YES
    2026-10-04 17:30:15.008 Df loginwindow[627:f9f6b2] [com.apple.loginwindow.logging:Standard] -[LWScreenLock startUnlock:] |      inform UA unlocked
    2026-10-04 17:30:15.008 Df loginwindow[627:f9f6b2] [com.apple.loginwindow.logging:Standard] -[LWScreenLock startUnlock:] |      _numFailedAuthAttempts = 0
    2026-10-04 17:30:15.008 Df loginwindow[627:f9f6b2] [com.apple.loginwindow.logging:Standard] -[LWScreenLock startUnlock:] |      after inform UA unlocked
    2026-10-04 17:30:15.008 Df loginwindow[627:10ed] [com.apple.loginwindow.logging:Standard] -[LWScreenLock startUnlock:]_block_invoke |      calling BrightenDisplayIfNeeded
    2026-10-04 17:30:15.019 Df loginwindow[627:f9f6b2] [com.apple.loginwindow.logging:Standard] -[LWScreenLock startUnlock:] | shared auth preload
    2026-10-04 17:30:15.019 Df loginwindow[627:f9f6b2] [com.apple.loginwindow.logging:Standard] -[LWScreenLockAuthentication authStateForUsername:returningBackOffTime:] | enter
    2026-10-04 17:30:15.031 Df loginwindow[627:10ed] [com.apple.loginwindow.logging:Standard] -[LWScreenLock startUnlock:]_block_invoke |     hiding password field
    2026-10-04 17:30:15.031 Df loginwindow[627:10ed] [com.apple.loginwindow.logging:Standard] -[LWScreenLock startUnlock:]_block_invoke |     calling showScreenLock:
    2026-10-04 17:30:15.052 Df loginwindow[627:f9f6b2] [com.apple.loginwindow.logging:Standard] -[LWScreenLock startUnlock:] | exit
    2026-10-04 17:30:15.260 Df loginwindow[627:f9e08e] [com.apple.loginwindow.logging:Standard] -[LWAuthServiceManager activateLAIfAppropriateWithCompletionBlock:]_block_invoke |      activateService:kLocalAuthenticationServiceProvider
    2026-10-04 17:30:15.260 Df loginwindow[627:f9e08e] [com.apple.loginUI:AuthenticationService] ============ [-[LUIAuthenticationManager activateService:withUserName:sessionUnlocked:shouldReset:withOptions:]] - com.apple.LocalAuthentication.Au
    2026-10-04 17:30:15.260 Df loginwindow[627:f9f6b2] [com.apple.loginUI:AuthenticationService] Service com.apple.LocalAuthentication.AuthenticationHintsProvider is already active for lsimons (sessionUnlocked: NO): ignoring additional call to 
    2026-10-04 17:30:15.263 Df loginwindow[627:f9ee59] [com.apple.loginwindow.logging:Standard] -[LWAuthServiceManager activateLAIfAppropriateWithCompletionBlock:]_block_invoke |      activateService:kLocalAuthenticationServiceProvider
    2026-10-04 17:30:15.263 Df loginwindow[627:f9ee59] [com.apple.loginUI:AuthenticationService] ============ [-[LUIAuthenticationManager activateService:withUserName:sessionUnlocked:shouldReset:withOptions:]] - com.apple.LocalAuthentication.Au
    2026-10-04 17:30:15.263 Df loginwindow[627:f9fc42] [com.apple.loginUI:AuthenticationService] Service com.apple.LocalAuthentication.AuthenticationHintsProvider is already active for lsimons (sessionUnlocked: NO): ignoring additional call to 
    2026-10-04 17:30:15.274 Df loginwindow[627:f9fc44] [com.apple.loginwindow.logging:Standard] -[LWScreenLock _checkAuthWithContinuityHints:localAuthenticationHints:username:password:completionBlock:]_block_invoke_2 | no activity semaphore, co
    2026-10-04 17:30:15.274 Df loginwindow[627:f9fc44] [com.apple.loginwindow.logging:Standard] -[LWScreenLockAuthentication checkAuthWithContinuityHints:localAuthenticationHints:username:password:completionBlock:]_block_invoke | enter
    2026-10-04 17:30:15.274 Df loginwindow[627:f9fc44] [com.apple.loginwindow.logging:Standard] -[LWScreenLockAuthentication checkAuthWithContinuityHints:localAuthenticationHints:username:password:completionBlock:]_block_invoke | Use Auth
    2026-10-04 17:30:15.274 Df loginwindow[627:f9fc44] [com.apple.loginwindow.logging:Standard] -[LWScreenLockAuthentication _authCopyRightsWithUsername:password:continuityHints:localAuthenticationHints:] | enter, using _authCopyRightsWithUsern
    2026-10-04 17:30:15.280 Df loginwindow[627:f9fc44] [com.apple.loginwindow.logging:Standard] -[LWScreenLockAuthentication _authCopyRightsWithUsername:password:continuityHints:localAuthenticationHints:] | Using localAuthentication hints
    2026-10-04 17:30:15.280 Df loginwindow[627:f9fc44] [com.apple.loginwindow.logging:Standard] -[LWScreenLockAuthentication _authCopyRightsWithUsername:password:continuityHints:localAuthenticationHints:] | username is provided elsewhere, Local
    2026-10-04 17:30:15.280 Df loginwindow[627:f9fc44] [com.apple.loginwindow.logging:Standard] -[LWScreenLockAuthentication _authCopyRightsWithUsername:password:continuityHints:localAuthenticationHints:] | Calling Auth
    2026-10-04 17:30:15.350 Df loginwindow[627:f9fc44] [com.apple.loginwindow.logging:Standard] -[LWScreenLockAuthentication _authCopyRightsWithUsername:password:continuityHints:localAuthenticationHints:] | Screensaver authorization succeeded
    2026-10-04 17:30:15.350 Df loginwindow[627:f9fc44] [com.apple.loginwindow.logging:Standard] -[LWScreenLockAuthentication _authCopyRightsWithUsername:password:continuityHints:localAuthenticationHints:] | Returning: 0
    2026-10-04 17:30:15.350 Df loginwindow[627:f9fc44] [com.apple.loginwindow.logging:Standard] -[LWScreenLockAuthentication _authSuccessUsingPassword:forUser:] | la
    2026-10-04 17:30:15.350 Df loginwindow[627:f9fc44] [com.apple.loginwindow.logging:Standard] -[LWScreenLockAuthentication _authSuccessUsingPassword:forUser:] | Screen unlock succeeded via auth service: la, did NOT unlock the user's keychain
    2026-10-04 17:30:15.350 Df loginwindow[627:10ed] [com.apple.loginwindow.logging:Standard] -[LWScreenLock _checkAuthWithContinuityHints:localAuthenticationHints:username:password:completionBlock:]_block_invoke_2 | calling switch on authresul
    2026-10-04 17:30:15.350 Df loginwindow[627:10ed] [com.apple.loginwindow.logging:Standard] -[LWAuthServiceManager _shouldDeactivateService:withError:passwordRequired:context:] | enter: com.apple.AutoUnlock.System.AuthenticationHintsProvider
    2026-10-04 17:30:15.350 Df loginwindow[627:10ed] [com.apple.loginUI:AuthenticationService] ============ [-[LUIAuthenticationManager deActivateService:withContext:]] - com.apple.AutoUnlock.System.AuthenticationHintsProvider ============
    2026-10-04 17:30:15.350 Df loginwindow[627:f9fc42] [com.apple.loginUI:AuthenticationService] Service com.apple.AutoUnlock.System.AuthenticationHintsProvider is not active, cancelling deactivate for (null)
    2026-10-04 17:30:15.352 Df loginwindow[627:10ed] [com.apple.loginwindow.logging:Standard] -[LWAuthServiceManager _shouldDeactivateService:withError:passwordRequired:context:] | enter: com.apple.AutoUnlock.System.AuthenticationHintsProvider
    2026-10-04 17:30:15.352 Df loginwindow[627:10ed] [com.apple.loginUI:AuthenticationService] ============ [-[LUIAuthenticationManager deActivateService:withContext:]] - com.apple.AutoUnlock.System.AuthenticationHintsProvider ============
    2026-10-04 17:30:15.352 Df loginwindow[627:f9ee59] [com.apple.loginUI:AuthenticationService] Service com.apple.AutoUnlock.System.AuthenticationHintsProvider is not active, cancelling deactivate for (null)
    2026-10-04 17:30:15.396 Df loginwindow[627:10ed] [com.apple.loginwindow.logging:Standard] -[LWScreenLock sendScreenUnlockedNotification] |      sendNotificationOf kScreenIsUnlocked
    2026-10-04 17:30:15.396 Df loginwindow[627:10ed] [com.apple.loginUI:AuthenticationService] ============ [-[LUIAuthenticationManager activateService:withUserName:sessionUnlocked:shouldReset:withOptions:]] - com.apple.CryptoTokenKit.Authentic
    2026-10-04 17:30:15.396 Df loginwindow[627:10ed] [com.apple.loginwindow.logging:Standard] -[LWAuthServiceManager _shouldDeactivateService:withError:passwordRequired:context:] | enter: com.apple.AutoUnlock.System.AuthenticationHintsProvider
    2026-10-04 17:30:15.396 Df loginwindow[627:10ed] [com.apple.loginUI:AuthenticationService] ============ [-[LUIAuthenticationManager deActivateService:withContext:]] - com.apple.AutoUnlock.System.AuthenticationHintsProvider ============
    2026-10-04 17:30:15.397 Df loginwindow[627:10ed] [com.apple.loginwindow.logging:Standard] -[SessionAgentNotificationCenter doSpecialNotificationHandling:] | Activated SANC_ScreenIsUnlocked AHPs
    2026-10-04 17:30:15.397 Df loginwindow[627:10ed] [com.apple.loginwindow.logging:Standard] -[SessionAgentNotificationCenter sendDistributedNotification:object:] | sendDistributedNotification: com.apple.screenIsUnlocked, with object:501
    2026-10-04 17:30:15.397 Df loginwindow[627:10ed] [com.apple.loginwindow.logging:Standard] -[SessionAgentNotificationCenter sendBSDNotification:object:] | sendBSDNotification: com.apple.sessionagent.screenIsUnlocked, with object:501
    2026-10-04 17:30:15.400 Df loginwindow[627:f9fc45] [com.apple.loginUI:AuthenticationService] Service com.apple.AutoUnlock.System.AuthenticationHintsProvider is not active, cancelling deactivate for (null)
    2026-10-04 17:30:15.438 Df loginwindow[627:f9fc42] [com.apple.loginUI:AuthenticationService] ============ [-[LUIAuthenticationManager activateService:withUserName:sessionUnlocked:shouldReset:withOptions:]] - com.apple.LocalAuthentication.Au

## After the unlock

The same binary ran `x02 cycle base 20 60` on the same bundle from
17:30:33 CEST. All 20 cycle saves and the final save succeeded
(`results/cycle-4g.jsonl`, `results/cycle-4g.err`).
