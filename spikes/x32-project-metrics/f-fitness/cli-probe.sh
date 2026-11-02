#!/usr/bin/env bash
# Runs read-only wb invocations (usage, help, stubs) and records output and exit status.
# Never runs claude, setup, image build, land, discard or any vm start/stop/suspend/reset.
W="$1"
cd /tmp || exit 1
while IFS= read -r line; do
  [ -z "$line" ] && continue
  read -r -a argv <<<"$line"
  echo "--- wb $line"
  "$W" "${argv[@]}" 2>&1 | head -14
  echo "exit=${PIPESTATUS[0]}"
done <<'LIST'
status
sessions
diff x
approve
policy show
audit tail
image list
cred list
vm
vm --help
vm status --help
vm bogus
project
project --help
project show --bogus
shell --help
--bogus
--isolated status
-C /nonexistent version
LIST
