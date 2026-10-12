# X08: the host state wb-hostd would keep per project user, used to create
# the users on boot 1 and recreate them on the fresh system disk g2.
# name uid gid user-GeneratedUID group-GeneratedUID
#
# The user GeneratedUIDs are the ones opendirectoryd assigned on boot 1:
# the first mkusers.sh set GeneratedUID after PrimaryGroupID, which dscl
# refuses (b1-06), so the host recorded what the guest had instead
# (CBBC08A1-... and 63095303-... were the intended ones).
USERS="
wbp1 601 601 5625CA53-D5B4-4774-A7FC-CD64637EE742 8A30754A-4E5A-469E-B14E-F9182656F981
wbp2 602 602 D1A05D50-CEA4-4BC2-BB27-4B02ED6D843A 196069B6-2BDC-4677-9AAB-A1A6E436823E
"
HOMES=/Volumes/wbhomes
