"""Print the log messages of a run-case.sh log record that match a regex.

Usage: logfind.py <log.ndjson> <regex> [max chars per message]
Prints timestamp, process and the message, one per line (newlines shown
as \\n). Run with python3 -I.
"""

import json
import re
import sys

path, pattern = sys.argv[1], re.compile(sys.argv[2], re.I)
width = int(sys.argv[3]) if len(sys.argv) > 3 else 400
for line in open(path, errors="replace"):
    try:
        e = json.loads(line)
    except ValueError:
        continue
    msg = e.get("eventMessage", "")
    if pattern.search(msg):
        proc = e.get("processImagePath", "").rsplit("/", 1)[-1]
        print(e.get("timestamp", "")[11:23], proc, msg.replace("\n", "\\n")[:width])
