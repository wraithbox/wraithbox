# X08 cycle 2: the host state wb-hostd would keep per project user, used to
# create the users on g1 (boot 3) and recreate them on g2 (boot 4). The
# homes are on the passphrase volume WBPass: the USB volume WBHomes is
# refused by TCC (c2-02, c2-03). PASS is the volume passphrase the host
# would keep and hand to wb-guestd at each boot.
# name uid gid user-GeneratedUID group-GeneratedUID
USERS="
wbq1 611 611 FEB034B1-B3D0-45A2-B4CB-20E64337C168 F7669BC6-8BED-4835-853F-3FAB79B65A66
wbq2 612 612 9BDC4F74-9188-4C55-A2F3-AB9337A66081 0D1715CB-07CE-4C87-B482-0C0356FD884C
"
HOMES=/private/var/wbhomes
H=$HOMES U1=wbq1 U2=wbq2 VOLNAME=WBPass PASS=x08-pass
