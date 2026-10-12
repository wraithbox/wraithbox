# X08 cycle 3a: homes on the APFS volume the host made (host-format.sh,
# data3.img). Users made on g2 (boot 5), recreated on g3 (boot 6) from this
# host state. name uid gid user-GeneratedUID group-GeneratedUID
USERS="
wbr1 621 621 B5E85314-4A02-4E80-9475-67A94E872BE3 32AB5FCD-9EB3-409E-8E67-2B3AA9203E30
wbr2 622 622 63DA8CD6-4BE9-4CB5-A74B-B1554D13C480 E3906395-A9B0-4D0E-89B0-29526C459D81
"
HOMES=/private/var/wbhomes
H=$HOMES U1=wbr1 U2=wbr2 VOLNAME=WBHost
