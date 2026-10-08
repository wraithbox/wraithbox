sqlite3 -separator ' | ' /private/var/protected/trustd/pinningrules.sqlite3 'select policyName, domainSuffix, labelRegex, transparentConnection from rules order by domainSuffix'
sqlite3 /private/var/protected/trustd/pinningrules.sqlite3 'select count(*) from rules'
sqlite3 /private/var/protected/trustd/pinningrules.sqlite3 'select key, ival from admin'
