package tty_test

import "golang.org/x/sys/unix"

// getTermios is the ioctl that reads a terminal's termios on darwin.
const getTermios = unix.TIOCGETA
