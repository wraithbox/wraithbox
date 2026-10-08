# X06 task: one xcodebuild action. Arguments come from the environment:
#   X06_NAME (log name), X06_DIR (project folder under ~/work),
#   X06_ARGS (everything after xcodebuild, split on spaces, "+" inside one
#   argument stands for a space), X06_DD (DerivedData under ~/work).
cd ~/work/"$X06_DIR" || exit 1
dd=~/work/"${X06_DD:-dd}"
log=~/work/"$X06_NAME".log
set --
for a in $X06_ARGS; do
  set -- "$@" "$(printf '%s' "$a" | tr '+' ' ')"
done
echo "xcodebuild $*"
t0=$(date +%s)
xcodebuild "$@" -derivedDataPath "$dd" > "$log" 2>&1 &
xp=$!
# X06_LIMIT: kill xcodebuild after that many seconds (a hang under the profile).
if [ -n "${X06_LIMIT:-}" ]; then ( sleep "$X06_LIMIT"; kill -KILL $xp 2>/dev/null && echo "RESULT killed after ${X06_LIMIT}s" ) & fi
wait $xp; rc=$?
t1=$(date +%s)
grep -E "error:|warning: .*(sign|Sign)|Test Suite|Test Case .*(passed|failed)|Executed|TEST (SUCCEEDED|FAILED)|BUILD (SUCCEEDED|FAILED)|\*\* |Testing failed|Failed to|Unable to|timed out|Underlying Error|Early unexpected exit|Restarting after" "$log" | grep -v '^    ' | head -40
echo "--- last lines"
tail -6 "$log"
echo "RESULT $X06_NAME exit=$rc $((t1 - t0))s"
exit $rc
