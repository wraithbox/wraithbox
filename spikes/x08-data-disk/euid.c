// X08: prints the real and effective uid, to see whether a setuid bit on the
// data volume takes effect. A copy of /usr/bin/id is killed outside the
// system volume, so this is built on the host (cc signs it ad hoc). Throwaway.
#include <stdio.h>
#include <unistd.h>

int main(void) {
    printf("ruid=%d euid=%d\n", (int)getuid(), (int)geteuid());
    return 0;
}
