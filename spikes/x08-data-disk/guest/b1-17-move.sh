# X08 boot 1: move the homes to the mount point outside /Volumes. The data
# volume's content is unchanged, only its mount point and the users'
# NFSHomeDirectory change. The Claude Code state that names the home path is
# rewritten as each user, as Claude Code would have written it there.
# Throwaway.
set -u
mp=/private/var/wbhomes
for n in wbp1 wbp2; do
  dscl . -change /Users/$n NFSHomeDirectory /Volumes/wbhomes/$n $mp/$n
done
dscacheutil -flushcache
for n in wbp1 wbp2; do
  sudo -u $n -H /bin/sh -c '
    n=$(id -un); cd "$HOME" || exit 1; echo "$n HOME=$HOME"
    printf "{\"numStartups\":3,\"projects\":{\"%s/sessions/s1\":{\"allowedTools\":[\"Bash(git status)\"]}}}\n" "$HOME" > .claude.json
    rm .claude/projects/-Volumes-wbhomes-$n-sessions-s1
    ln -s ../../.claude/projects/wb-project .claude/projects/-private-var-wbhomes-$n-sessions-s1
    ls -la .claude/projects'
done
sync
