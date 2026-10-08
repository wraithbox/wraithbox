#!/bin/sh
# fetch a guest file into results/: fetch.sh <guest path> <results name>
spikes/x04-tls-inspection/r.sh runs "cat $1" 60 | tail -n +2 > spikes/x04-tls-inspection/results/$2
