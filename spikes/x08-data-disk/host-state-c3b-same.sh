# X08 cycle 3b: homes on an HFS+ volume the guest formats on g2 (boot 5,
# data4.img), recreated on g3 (boot 6).
# name uid gid user-GeneratedUID group-GeneratedUID
USERS="
wbs1 631 631 03BEAC8D-42A1-4880-9906-6B2780AF6D66 9E338F45-5656-4673-9091-A11D2E2D4EBA
wbs2 632 632 B75A7ED7-5ECC-4E6D-92DC-2F5710A2E7BD 37D00E7A-78E9-463F-A097-EB9F22D1ED60
"
HOMES=/private/var/wbhfs
H=$HOMES U1=wbs1 U2=wbs2 VOLNAME=WBHfs
