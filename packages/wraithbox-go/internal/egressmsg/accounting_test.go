package egressmsg

import (
	"testing"

	egressv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/egress/v1"
)

func debitRec(name string) *egressv1.AccountingRecord {
	return &egressv1.AccountingRecord{Kind: &egressv1.AccountingRecord_WildcardDebit{WildcardDebit: &egressv1.WildcardDebit{Name: name}}}
}

func entryRec(name string, addr uint32) *egressv1.AccountingRecord {
	return &egressv1.AccountingRecord{Kind: &egressv1.AccountingRecord_MappingEntry{MappingEntry: &egressv1.MappingEntry{Name: name, Address: addr}}}
}

func releaseRec(addr uint32) *egressv1.AccountingRecord {
	return &egressv1.AccountingRecord{Kind: &egressv1.AccountingRecord_MappingRelease{MappingRelease: &egressv1.MappingRelease{Address: addr}}}
}

func proxydRec(vm, name string) *egressv1.AccountingRecord {
	return &egressv1.AccountingRecord{Kind: &egressv1.AccountingRecord_ProxydWildcardName{ProxydWildcardName: &egressv1.ProxydWildcardName{Vm: vm, Name: name}}}
}

func TestDecodeAccountingRecord(t *testing.T) {
	for _, tc := range []struct {
		name string
		rec  *egressv1.AccountingRecord
		from Sender
		fd   bool
		want Check
		kind AccountingKind
	}{
		{"debit", debitRec("x.example.com"), SenderNetd, false, "", KindWildcardDebit},
		{"entry", entryRec("x.example.com", addr1), SenderNetd, false, "", KindMappingEntry},
		{"release", releaseRec(addr1), SenderNetd, false, "", KindMappingRelease},
		{"proxyd name", proxydRec("isolated", "x.example.com"), SenderProxyd, false, "", KindProxydWildcardName},
		{"debit with descriptor", debitRec("x.example.com"), SenderNetd, true, CheckMessageForm, 0},
		{"debit from proxyd", debitRec("x.example.com"), SenderProxyd, false, CheckMessageForm, 0},
		{"entry from proxyd", entryRec("x.example.com", addr1), SenderProxyd, false, CheckMessageForm, 0},
		{"release from proxyd", releaseRec(addr1), SenderProxyd, false, CheckMessageForm, 0},
		{"proxyd name from netd", proxydRec("work", "x.example.com"), SenderNetd, false, CheckMessageForm, 0},
		{"debit capital", debitRec("X.example.com"), SenderNetd, false, CheckNameForm, 0},
		{"entry bad name", entryRec("x..example", addr1), SenderNetd, false, CheckNameForm, 0},
		{"entry outside range", entryRec("x.example", 0x08080808), SenderNetd, false, CheckSyntheticRange, 0},
		{"release outside range", releaseRec(0), SenderNetd, false, CheckSyntheticRange, 0},
		{"proxyd bad VM", proxydRec("build", "x.example"), SenderProxyd, false, CheckVMSlot, 0},
		{"proxyd bad name", proxydRec("work", "x.example."), SenderProxyd, false, CheckNameForm, 0},
		{"no kind", &egressv1.AccountingRecord{}, SenderNetd, false, CheckMessageForm, 0},
		{"no sender", debitRec("x.example.com"), 0, false, CheckMessageForm, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, r := DecodeAccountingRecord(marshal(t, tc.rec), tc.fd, tc.from)
			if tc.want != "" {
				wantCheck(t, r, tc.want)
				return
			}
			if r != nil {
				t.Fatal(r)
			}
			if got.Kind != tc.kind {
				t.Fatalf("kind %v, want %v", got.Kind, tc.kind)
			}
		})
	}
	// A clock record's field number, kept, is refused by this build.
	_, r := DecodeAccountingRecord(withField(nil, 5), false, SenderNetd)
	wantCheck(t, r, CheckMessageForm)
	_, r = DecodeAccountingRecord(withNestedField(t, marshal(t, debitRec("a.example")), 2), false, SenderNetd)
	wantCheck(t, r, CheckMessageForm)
}
