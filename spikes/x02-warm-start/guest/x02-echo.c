// X02-warm-start guest probe. Throwaway. Runs as a root LaunchDaemon in
// the macOS guest and listens on vsock port 1024. One line per request:
//   ping            -> "pong <boottime_sec> <uptime_ms>"
//   net <nonce>     -> writes one raw Ethernet frame (ethertype 0x88B5,
//                      payload "x02-net-<nonce>") out en0 through BPF,
//                      replies "sent <n>" or "err <errno>"
//   halt            -> replies "ok", then shuts the guest down
#include <errno.h>
#include <fcntl.h>
#include <net/bpf.h>
#include <net/if.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/ioctl.h>
#include <sys/socket.h>
#include <sys/sysctl.h>
#include <sys/time.h>
#include <sys/vsock.h>
#include <time.h>
#include <unistd.h>

#define PORT 1024

static long long boottime_sec(void) {
    struct timeval tv;
    size_t len = sizeof(tv);
    int mib[2] = {CTL_KERN, KERN_BOOTTIME};
    if (sysctl(mib, 2, &tv, &len, NULL, 0) != 0) return -1;
    return (long long)tv.tv_sec;
}

static long long uptime_ms(void) {
    return (long long)(clock_gettime_nsec_np(CLOCK_MONOTONIC_RAW) / 1000000ULL);
}

static int send_frame(const char *nonce) {
    char dev[32];
    int fd = -1;
    for (int i = 0; i < 256 && fd < 0; i++) {
        snprintf(dev, sizeof(dev), "/dev/bpf%d", i);
        fd = open(dev, O_RDWR);
    }
    if (fd < 0) return -errno;
    struct ifreq ifr;
    memset(&ifr, 0, sizeof(ifr));
    strlcpy(ifr.ifr_name, "en0", sizeof(ifr.ifr_name));
    if (ioctl(fd, BIOCSETIF, &ifr) != 0) {
        int e = errno;
        close(fd);
        return -e;
    }
    unsigned char frame[128];
    memset(frame, 0, sizeof(frame));
    memset(frame, 0xff, 6);              // dst broadcast
    memcpy(frame + 6, "\x02\x00\x00\x00\x00\x02", 6);  // src (BPF does not rewrite)
    frame[12] = 0x88;
    frame[13] = 0xb5;
    int n = snprintf((char *)frame + 14, sizeof(frame) - 14, "x02-net-%s", nonce);
    ssize_t w = write(fd, frame, 14 + n + 1 < 60 ? 60 : 14 + n + 1);
    int e = errno;
    close(fd);
    return w < 0 ? -e : (int)w;
}

static void serve(int c) {
    char buf[256];
    size_t used = 0;
    for (;;) {
        ssize_t r = read(c, buf + used, sizeof(buf) - 1 - used);
        if (r <= 0) return;
        used += (size_t)r;
        buf[used] = 0;
        char *nl;
        while ((nl = strchr(buf, '\n')) != NULL) {
            *nl = 0;
            char out[128];
            if (strcmp(buf, "ping") == 0) {
                snprintf(out, sizeof(out), "pong %lld %lld\n", boottime_sec(), uptime_ms());
            } else if (strncmp(buf, "net ", 4) == 0) {
                int rc = send_frame(buf + 4);
                if (rc < 0)
                    snprintf(out, sizeof(out), "err %d\n", -rc);
                else
                    snprintf(out, sizeof(out), "sent %d\n", rc);
            } else if (strcmp(buf, "halt") == 0) {
                write(c, "ok\n", 3);
                close(c);
                execl("/sbin/shutdown", "shutdown", "-h", "now", (char *)NULL);
                _exit(1);
            } else {
                snprintf(out, sizeof(out), "unknown\n");
            }
            write(c, out, strlen(out));
            size_t rest = used - (size_t)(nl + 1 - buf);
            memmove(buf, nl + 1, rest);
            used = rest;
            buf[used] = 0;
        }
        if (used >= sizeof(buf) - 1) return;
    }
}

int main(void) {
    int s = socket(AF_VSOCK, SOCK_STREAM, 0);
    if (s < 0) {
        perror("socket");
        return 1;
    }
    struct sockaddr_vm a;
    memset(&a, 0, sizeof(a));
    a.svm_len = sizeof(a);
    a.svm_family = AF_VSOCK;
    a.svm_cid = VMADDR_CID_ANY;
    a.svm_port = PORT;
    while (bind(s, (struct sockaddr *)&a, sizeof(a)) != 0) {
        perror("bind");
        sleep(1);
    }
    if (listen(s, 8) != 0) {
        perror("listen");
        return 1;
    }
    fprintf(stderr, "x02-echo listening on vsock %d, boottime %lld\n", PORT, boottime_sec());
    for (;;) {
        int c = accept(s, NULL, NULL);
        if (c < 0) continue;
        serve(c);
        close(c);
    }
}
