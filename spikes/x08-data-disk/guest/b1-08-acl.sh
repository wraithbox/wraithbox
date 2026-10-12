# X08 boot 1: b1-07's ACL denied write but not append, so the append test
# passed. Add a file whose ACL denies the user write and append. Throwaway.
set -u
for n in wbp1 wbp2; do
  sudo -u $n -H /bin/sh -c '
    cd "$HOME"; n=$(id -un)
    echo acl2 > acl-deny
    chmod +a "user:$n deny write,append,writeattr,writeextattr,delete" acl-deny
    echo "$n append to acl-deny: $(echo x >> acl-deny 2>&1 && echo ALLOWED || echo refused)"
    echo "$n overwrite acl-deny: $( (echo y > acl-deny) 2>&1 && echo ALLOWED || echo refused)"
    ls -le acl-deny acl-file'
done
