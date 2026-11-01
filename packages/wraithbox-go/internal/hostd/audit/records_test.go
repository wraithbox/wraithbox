package audit

import (
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guesttext"
)

// schema is the subset of OCSF 1.8.0 in testdata: the required and the
// known attributes of each class and object the writer uses.
type schema struct {
	Version string `json:"version"`
	Classes map[string]struct {
		UID         int      `json:"uid"`
		CategoryUID int      `json:"category_uid"`
		Required    []string `json:"required"`
		ActivityIDs []int    `json:"activity_ids"`
		Attributes  []string `json:"attributes"`
	} `json:"classes"`
	Objects map[string]struct {
		Required   []string `json:"required"`
		AtLeastOne []string `json:"at_least_one"`
		Attributes []string `json:"attributes"`
	} `json:"objects"`
}

func loadSchema(t *testing.T) schema {
	t.Helper()
	data, err := os.ReadFile("testdata/ocsf-1.8.0-subset.json")
	if err != nil {
		t.Fatal(err)
	}
	var s schema
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatal(err)
	}
	if s.Version != OCSFVersion {
		t.Fatalf("testdata is OCSF %s, the writer writes %s", s.Version, OCSFVersion)
	}
	return s
}

// everyRecord is one record of each constructor.
func everyRecord() map[string]Record {
	code := 1
	since := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	state := &ProjectState{Realm: "github.com", Location: "o/r", Name: "r", Placement: "work", URLs: []string{"https://github.com/o/r"}}
	return map[string]Record{
		"DataDiskChanged": DataDiskChanged("work", "wb vm reset-data", "vm-reset-data", "data disk reset",
			DataDisk{OldVolumeUUID: "a", NewVolumeUUID: "b"}),
		"ProjectChanged": ProjectChanged("wb trust", "project-trust", ProjectChange{ID: "p1", Before: state, After: state},
			&Peer{UID: 501, PID: 9}),
		"GitChecked accept": GitChecked("accept", "host-git-version", Git{Binary: "/usr/bin/git", Version: "2.50", Minimum: "2.45"}),
		"GitChecked refuse": GitChecked("refuse", "host-git-version", Git{Binary: "/usr/bin/git", Reason: "too old"}),
		"VMStarting":        VMStarting("work", 1, "vm-start", Resources{CPUCount: 4, MemoryMiB: 8192}),
		"VMRunning":         VMRunning("work", 1, "vm-start"),
		"VMStopping":        VMStopping("work", 1, "vm-stop", "requested"),
		"VMStopped":         VMStopped("isolated", 2, "vm-stop", "guest", true),
		"ShellOpened":       ShellOpened("work", 1, "shell-open", Session{ID: "s1", ProjectID: "p1", User: "wb-p1"}, nil),
		"ShellEnded":        ShellEnded("work", 1, "end", "shell-ended", Session{ID: "s1", ExitCode: &code}),
		"ShellEnded lose":   ShellEnded("work", 1, "lose", "shell-lost", Session{ID: "s1"}),
		"ProcessesLeft":     ProcessesLeft("work", 1, "kill-processes-left", Kill{ID: 3, ProjectID: "p1"}, "7"),
		"KillClosed":        KillClosed("work", 1, "kill-cold-stop", Kill{ID: 3, ProjectID: "p1"}, "vm-stopped"),
		"WriteFailed":       WriteFailed(since, "no space left on device"),
	}
}

func classKey(uid int) string {
	switch uid {
	case ClassDeviceConfigStateChange:
		return "device_config_state_change"
	case ClassDetectionFinding:
		return "detection_finding"
	case ClassApplicationLifecycle:
		return "application_lifecycle"
	}
	return ""
}

// objectOf is the OCSF object of each attribute that holds one.
var objectOf = map[string]string{
	"metadata": "metadata", "device": "device", "app": "product", "finding_info": "finding_info",
}

// TestClassesRequired checks each record against OCSF 1.8.0: every
// required attribute of its class and of its objects is set, every
// attribute is one the class or object has, and the type_uid is the
// class's (S09-11-audit, "Classes").
func TestClassesRequired(t *testing.T) {
	s := loadSchema(t)
	for name, r := range everyRecord() {
		t.Run(name, func(t *testing.T) { checkRecord(t, s, r) })
	}
}

func checkRecord(t *testing.T, s schema, r Record) {
	t.Helper()
	h := r.Event.head()
	h.Time = 1
	h.Metadata = Metadata{Product: Product{Name: productName}, Version: OCSFVersion, LogName: "audit"}
	got := object(t, r.Event)
	class, ok := s.Classes[classKey(h.ClassUID)]
	if !ok {
		t.Fatalf("class %d isn't one of the writer's", h.ClassUID)
	}
	if h.CategoryUID != class.CategoryUID || h.TypeUID != h.ClassUID*100+h.ActivityID ||
		!slices.Contains(class.ActivityIDs, h.ActivityID) {
		t.Errorf("category %d, type %d, activity %d don't fit class %d", h.CategoryUID, h.TypeUID, h.ActivityID, class.UID)
	}
	checkAttrs(t, "class", got, class.Required, nil, class.Attributes)
	for attr, obj := range objectOf {
		if raw, ok := got[attr]; ok {
			def := s.Objects[obj]
			checkAttrs(t, attr, object(t, raw), def.Required, def.AtLeastOne, def.Attributes)
		}
	}
}

// object is v as JSON, by attribute.
func object(t *testing.T, v any) map[string]json.RawMessage {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var o map[string]json.RawMessage
	if err := json.Unmarshal(data, &o); err != nil {
		t.Fatal(err)
	}
	return o
}

func checkAttrs(t *testing.T, what string, got map[string]json.RawMessage, required, oneOf, known []string) {
	t.Helper()
	for _, a := range required {
		if v, ok := got[a]; !ok || string(v) == "null" {
			t.Errorf("%s: required %s is missing", what, a)
		}
	}
	if len(oneOf) > 0 && !slices.ContainsFunc(oneOf, func(a string) bool { _, ok := got[a]; return ok }) {
		t.Errorf("%s: none of %v is set", what, oneOf)
	}
	for a := range got {
		if !slices.Contains(known, a) {
			t.Errorf("%s: %s isn't an OCSF attribute", what, a)
		}
	}
}

// TestNoCredentialFields walks every type a record holds and fails on a
// field named for a credential, or one that could hold any value, so no
// record can carry one (S09-11-audit, "Never recorded";
// SEC04-no-guest-secrets).
func TestNoCredentialFields(t *testing.T) {
	seen := map[reflect.Type]bool{}
	var problems []string
	for _, e := range []Event{&ConfigChange{}, &AppLifecycle{}, &Finding{}} {
		typ := reflect.TypeOf(e).Elem()
		problems = walk(typ, typ.Name(), seen, problems)
	}
	for _, p := range problems {
		t.Error(p)
	}
}

var credentialWords = []string{"token", "secret", "password", "key"}

// walk returns problems with what it found in typ added.
func walk(typ reflect.Type, path string, seen map[reflect.Type]bool, problems []string) []string {
	for typ.Kind() == reflect.Pointer || typ.Kind() == reflect.Slice {
		typ = typ.Elem()
	}
	if typ.Kind() == reflect.Map || typ.Kind() == reflect.Interface {
		return append(problems, path+" is a "+typ.Kind().String()+", which can hold any field")
	}
	if typ.Kind() != reflect.Struct || seen[typ] {
		return problems
	}
	seen[typ] = true
	for f := range typ.Fields() {
		name := strings.ToLower(f.Name + " " + f.Tag.Get("json"))
		for _, w := range credentialWords {
			if strings.Contains(name, w) {
				problems = append(problems, path+"."+f.Name+" is named for a credential: "+w)
			}
		}
		problems = walk(f.Type, path+"."+f.Name, seen, problems)
	}
	return problems
}

// TestCredentialWalkFails feeds the walk a type with a credential field
// and a map, and sees both fail.
func TestCredentialWalkFails(t *testing.T) {
	type bad struct {
		APIToken string
		Extra    map[string]string
	}
	problems := walk(reflect.TypeFor[bad](), "bad", map[reflect.Type]bool{}, nil)
	if len(problems) != 2 {
		t.Errorf("problems = %q, want the token and the map", problems)
	}
}

// TestGuestLabel: guest text loses the escaped code points and is cut
// to valid UTF-8.
func TestGuestLabel(t *testing.T) {
	r := ProcessesLeft("work", 1, "kill-processes-left", Kill{ID: 1}, "7\x1b[31m\u202e"+strings.Repeat("é", 300))
	got := r.Event.head().Unmapped.GuestLabel.ProcessesLeft
	if strings.ContainsAny(got, "\x1b\u202e") || len(got) > maxGuestLabel || !strings.HasPrefix(got, "7[31m") {
		t.Errorf("label = %q", got)
	}
	if !strings.HasSuffix(got, "é") {
		t.Errorf("label %q isn't cut at a whole rune", got)
	}
}

// FuzzGuestLabel: whatever bytes the guest sends, the label is valid
// UTF-8, at most maxGuestLabel bytes, keeps none of the escaped code
// points, and its record marshals to JSON.
func FuzzGuestLabel(f *testing.F) {
	for _, seed := range []string{"", "7", "7\x1b[31m‮", strings.Repeat("é", 300), "\xff\xfe", "a\x00b"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, s string) {
		got := guestLabel(s)
		if !utf8.ValidString(got) || len(got) > maxGuestLabel || guesttext.Strip(got) != got {
			t.Fatalf("guestLabel(%q) = %q", s, got)
		}
		r := ProcessesLeft("work", 1, "kill-processes-left", Kill{ID: 1}, s)
		if _, err := json.Marshal(r.Event); err != nil {
			t.Fatalf("the record of %q doesn't marshal: %v", s, err)
		}
	})
}
