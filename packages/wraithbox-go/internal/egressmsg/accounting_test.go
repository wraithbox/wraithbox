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
		{"debit from proxyd", debitRec("x.example.com"), SenderProxyd, false, CheckMessageForm, KindWildcardDebit},
		{"entry from proxyd", entryRec("x.example.com", addr1), SenderProxyd, false, CheckMessageForm, KindMappingEntry},
		{"release from proxyd", releaseRec(addr1), SenderProxyd, false, CheckMessageForm, KindMappingRelease},
		{"proxyd name from netd", proxydRec("work", "x.example.com"), SenderNetd, false, CheckMessageForm, KindProxydWildcardName},
		{"debit capital", debitRec("X.example.com"), SenderNetd, false, CheckNameForm, KindWildcardDebit},
		{"entry bad name", entryRec("x..example", addr1), SenderNetd, false, CheckNameForm, KindMappingEntry},
		{"entry outside range", entryRec("x.example", 0x08080808), SenderNetd, false, CheckSyntheticRange, KindMappingEntry},
		{"release outside range", releaseRec(0), SenderNetd, false, CheckSyntheticRange, KindMappingRelease},
		{"proxyd bad VM", proxydRec("build", "x.example"), SenderProxyd, false, CheckVMSlot, KindProxydWildcardName},
		{"proxyd bad name", proxydRec("work", "x.example."), SenderProxyd, false, CheckNameForm, KindProxydWildcardName},
		{"no kind", &egressv1.AccountingRecord{}, SenderNetd, false, CheckMessageForm, 0},
		{"no sender", debitRec("x.example.com"), 0, false, CheckMessageForm, KindWildcardDebit},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, r := DecodeAccountingRecord(marshal(t, tc.rec), tc.fd, tc.from)
			if got.Kind != tc.kind {
				t.Fatalf("kind %v, want %v", got.Kind, tc.kind)
			}
			if tc.want == "" {
				if r != nil {
					t.Fatal(r)
				}
				return
			}
			wantCheck(t, r, tc.want)
			// A refused record holds its kind and no fields.
			if got != (AccountingRecord{Kind: tc.kind}) {
				t.Fatalf("refused record %+v, want only kind %v", got, tc.kind)
			}
		})
	}
	// A clock record's field number, kept, is refused by this build.
	_, r := DecodeAccountingRecord(withField(nil, 5), false, SenderNetd)
	wantCheck(t, r, CheckMessageForm)
	_, r = DecodeAccountingRecord(withNestedField(t, marshal(t, debitRec("a.example")), 2), false, SenderNetd)
	wantCheck(t, r, CheckMessageForm)
}
