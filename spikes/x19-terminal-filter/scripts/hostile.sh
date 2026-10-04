#!/bin/sh
# Prints sequences that act on the host terminal, between visible text.
# Run through the relay: only the text and the color should arrive.
printf 'start\n'
printf 'clipboard:\033]52;c;eDE5LWhvc3RpbGU=\007:end\n'
printf 'iterm2 file:\033]1337;File=name=eDE5LnR4dA==;size=4:eDE5Cg==\007:end\n'
printf 'notify:\033]9;wb approve: allow evil.example? press y\007:end\n'
printf 'ghostty notify:\033]777;notify;wb;approve evil.example\007:end\n'
printf 'file link:\033]8;;file:///System/Applications/Calculator.app\007calc\033]8;;\007:end\n'
printf 'web link:\033]8;;https://example.com\007example\033]8;;\007:end\n'
printf 'kitty graphics:\033_Ga=T,t=f;L2V0Yy9ob3N0cw==\033\\:end\n'
printf 'tmux:\033Ptmux;\033\033]52;c;eA==\007\033\\:end\n'
printf 'title report:\033[21t:end\n'
printf 'window move:\033[3;0;0t:end\n'
printf 'color: \033[31mred\033[0m\n'
printf 'done\n'
