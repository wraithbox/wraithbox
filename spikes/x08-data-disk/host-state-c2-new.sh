# X08 cycle 2: the same users with the same UID and GID but new
# GeneratedUIDs, for the recreation on g3 (boot 5).
# name uid gid user-GeneratedUID group-GeneratedUID
USERS="
wbq1 611 611 23620887-8815-4BFC-A935-B2449D5EA24D 025DA731-3B39-44A5-BE35-06EE6DDEB453
wbq2 612 612 D8AC616C-EA0F-4C1F-B654-FDD4F02ED076 6E9BD251-E582-4258-8269-8548D3C1D761
"
HOMES=/private/var/wbhomes
H=$HOMES U1=wbq1 U2=wbq2 VOLNAME=WBPass PASS=x08-pass
