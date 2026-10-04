818312 bytes of output, 334567 bytes of text

| Sequence | Count | Recordings | Decision | Example |
|---|---|---|---|---|
| `C0 0x0a LF` | 27365 | 7 | pass (c0-allowed) | `\x0a` |
| `C0 0x0d CR` | 41007 | 7 | pass (c0-allowed) | `\x0d` |
| `C0 0x0f SI` | 45 | 7 | pass (c0-allowed) | `\x0f` |
| `CSI 16t` | 6 | 6 | pass (csi-size-report) | `\x1b[16t` |
| `CSI <u` | 42 | 5 | pass (csi-kitty-keyboard) | `\x1b[<u` |
| `CSI >0q` | 12 | 7 | pass (csi-xtversion) | `\x1b[>0q` |
| `CSI >4;2m` | 32 | 5 | pass (csi-modify-other-keys) | `\x1b[>4;2m` |
| `CSI >4m` | 14 | 7 | pass (csi-modify-other-keys) | `\x1b[>4m` |
| `CSI >5u` | 32 | 5 | pass (csi-kitty-keyboard) | `\x1b[>5u` |
| `CSI ?1000h` | 10 | 1 | pass (csi-private-mode) | `\x1b[?1000h` |
| `CSI ?1000l` | 14 | 7 | pass (csi-private-mode) | `\x1b[?1000l` |
| `CSI ?1002h` | 10 | 1 | pass (csi-private-mode) | `\x1b[?1002h` |
| `CSI ?1002l` | 14 | 7 | pass (csi-private-mode) | `\x1b[?1002l` |
| `CSI ?1003h` | 10 | 1 | pass (csi-private-mode) | `\x1b[?1003h` |
| `CSI ?1003l` | 14 | 7 | pass (csi-private-mode) | `\x1b[?1003l` |
| `CSI ?1004h` | 12 | 7 | pass (csi-private-mode) | `\x1b[?1004h` |
| `CSI ?1004l` | 13 | 7 | pass (csi-private-mode) | `\x1b[?1004l` |
| `CSI ?1006h` | 10 | 1 | pass (csi-private-mode) | `\x1b[?1006h` |
| `CSI ?1006l` | 14 | 7 | pass (csi-private-mode) | `\x1b[?1006l` |
| `CSI ?1016$p` | 6 | 6 | pass (csi-mode-query) | `\x1b[?1016$p` |
| `CSI ?1016l` | 8 | 7 | pass (csi-private-mode) | `\x1b[?1016l` |
| `CSI ?1049h` | 1 | 1 | pass (csi-private-mode) | `\x1b[?1049h` |
| `CSI ?1049l` | 1 | 1 | pass (csi-private-mode) | `\x1b[?1049l` |
| `CSI ?2004h` | 49 | 7 | pass (csi-private-mode) | `\x1b[?2004h` |
| `CSI ?2004l` | 13 | 7 | pass (csi-private-mode) | `\x1b[?2004l` |
| `CSI ?2026$p` | 10 | 6 | pass (csi-mode-query) | `\x1b[?2026$p` |
| `CSI ?2026h` | 3564 | 6 | pass (csi-private-mode) | `\x1b[?2026h` |
| `CSI ?2026l` | 3564 | 6 | pass (csi-private-mode) | `\x1b[?2026l` |
| `CSI ?2031h` | 12 | 7 | pass (csi-private-mode) | `\x1b[?2031h` |
| `CSI ?2031l` | 13 | 7 | pass (csi-private-mode) | `\x1b[?2031l` |
| `CSI ?25h` | 20 | 7 | pass (csi-private-mode) | `\x1b[?25h` |
| `CSI ?25l` | 8 | 7 | pass (csi-private-mode) | `\x1b[?25l` |
| `CSI ?u` | 7 | 7 | pass (csi-kitty-keyboard) | `\x1b[?u` |
| `CSI A` | 8076 | 7 | pass (csi-cursor-edit) | `\x1b[4A` |
| `CSI B` | 7886 | 7 | pass (csi-cursor-edit) | `\x1b[4B` |
| `CSI C` | 8824 | 7 | pass (csi-cursor-edit) | `\x1b[1C` |
| `CSI D` | 3886 | 7 | pass (csi-cursor-edit) | `\x1b[1D` |
| `CSI G` | 9294 | 7 | pass (csi-cursor-edit) | `\x1b[2G` |
| `CSI H` | 2466 | 6 | pass (csi-cursor-edit) | `\x1b[H` |
| `CSI J` | 3 | 1 | pass (csi-cursor-edit) | `\x1b[2J` |
| `CSI K` | 2373 | 7 | pass (csi-cursor-edit) | `\x1b[2K` |
| `CSI S` | 42 | 1 | pass (csi-cursor-edit) | `\x1b[2S` |
| `CSI T` | 2 | 1 | pass (csi-cursor-edit) | `\x1b[3T` |
| `CSI c` | 22 | 7 | pass (csi-device-attributes) | `\x1b[c` |
| `CSI m (SGR)` | 19585 | 7 | pass (csi-sgr) | `\x1b[38;2;144;109;0m` |
| `CSI r` | 103 | 7 | pass (csi-cursor-edit) | `\x1b[r` |
| `ESC (B` | 45 | 7 | pass (esc-charset) | `\x1b(B` |
| `ESC 7` | 15 | 7 | pass (esc-decsc) | `\x1b7` |
| `ESC 8` | 15 | 7 | pass (esc-decrc) | `\x1b8` |
| `OSC 0` | 186 | 6 | pass (osc-title) | `\x1b]0;\xe2\x9c\xb3 Claude Code\x07` |
| `OSC 52` | 1 | 1 | DROP (osc52-clipboard) | `\x1b]52;c;cGluZWFwcGxl\x07` |
| `OSC 777` | 4 | 2 | rewrite (osc-notification-to-bel) | `\x1b]777;notify;Claude Code;Claude needs your permission\x07` |
| `OSC 8` | 90 | 5 | pass (osc8-http) | `\x1b]8;id=zaxmda;https://code.claude.com/docs/en/security\x07` |
| `OSC 9` | 2 | 1 | rewrite (osc-notification-to-bel) | `\x1b]9;Claude needs your permission\x07` |
| `OSC 9;4 (progress)` | 33 | 4 | pass (osc9-progress) | `\x1b]9;4;0;\x07` |
| `apc` | 6 | 6 | DROP (apc) | `\x1b_Gi=31,s=1,v=1,a=q,t=d,f=24;AAAA\x1b\` |
