#!/bin/sh
# halt.sh: run as root in the test guest. Shuts it down, which ends the
# provisioning boot on the host. Throwaway spike helper for I228.
shutdown -h +0 >/dev/null 2>&1 &
echo "shutting down"
