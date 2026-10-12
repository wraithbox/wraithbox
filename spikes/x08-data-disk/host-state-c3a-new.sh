# X08 cycle 3a: the same users, same UID and GID, new GeneratedUIDs, for the
# recreation on g1 (boot 7), a system disk that never had them.
# name uid gid user-GeneratedUID group-GeneratedUID
USERS="
wbr1 621 621 97432B56-D717-4489-B514-BDC4DDE9828A C3F2866B-A2E8-4FB0-B971-5AD378EE22D7
wbr2 622 622 DBD253F2-DD49-4505-B5FA-1935D6346E8E 82BD1E5A-33AA-45D2-A36C-8E11CA4DEB41
"
HOMES=/private/var/wbhomes
H=$HOMES U1=wbr1 U2=wbr2 VOLNAME=WBHost
