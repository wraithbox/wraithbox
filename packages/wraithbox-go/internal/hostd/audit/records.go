package audit

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guesttext"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/vmbundle"
)

// Record is one record for the writer: the VM it belongs to, and its
// event. A record with no Slot belongs to no VM, and a failed write of
// it stops every VM (S09-11-audit, "Failed writes").
type Record struct {
	Slot       string
	Generation uint64
	// Stop marks a VM's stop record. The writer syncs the file before it
	// writes one.
	Stop  bool
	Event Event
}

// Fields are a record's attributes that OCSF has none for, in its
// unmapped object: the decision and the rule that made it (SEC10-audit),
// the VM, and what each kind of record adds. No field is named for a
// credential, and none can hold one (S09-11-audit, "Never recorded").
type Fields struct {
	Decision   string         `json:"decision"`
	Rule       string         `json:"rule"`
	VM         string         `json:"vm,omitempty"`
	Generation uint64         `json:"generation,omitempty"`
	Command    string         `json:"command,omitempty"`
	Cause      string         `json:"cause,omitempty"`
	Peer       *Peer          `json:"peer,omitempty"`
	Resources  *Resources     `json:"resources,omitempty"`
	DataDisk   *DataDisk      `json:"data_disk,omitempty"`
	Project    *ProjectChange `json:"project,omitempty"`
	Git        *Git           `json:"git,omitempty"`
	Session    *Session       `json:"session,omitempty"`
	Kill       *Kill          `json:"kill,omitempty"`
	// GuestLabel holds what the guest reported, stored as its untrusted
	// label (S09-11-audit, "Fields").
	GuestLabel *GuestLabel `json:"guest_label,omitempty"`
}

// Peer is the wb that asked, by the user ID and process ID localipc
// checked (SEC12-least-privilege).
type Peer struct {
	UID int `json:"uid"`
	PID int `json:"pid"`
}

// Resources are the CPU count and memory a VM starts with.
type Resources struct {
	CPUCount  uint32 `json:"cpu_count"`
	MemoryMiB uint64 `json:"memory_mib"`
}

// DataDisk is a data disk change: the volume UUID before and after.
type DataDisk struct {
	OldVolumeUUID string `json:"old_volume_uuid,omitempty"`
	NewVolumeUUID string `json:"new_volume_uuid,omitempty"`
}

// ProjectState is a project's settings, as `wb project` changes them
// (S09-11-audit, "Project settings"). The URLs are the recorded ones,
// which never hold a user name or password (internal/gitremote).
type ProjectState struct {
	Realm       string   `json:"realm"`
	Location    string   `json:"location"`
	Name        string   `json:"name"`
	Placement   string   `json:"placement"`
	ConfigTrust bool     `json:"config_trust"`
	URLs        []string `json:"urls"`
	RootCommits []string `json:"root_commits"`
}

// ProjectChange is a project's settings before and after a change.
// After is nil for a removal.
type ProjectChange struct {
	ID     string        `json:"id"`
	Before *ProjectState `json:"before"`
	After  *ProjectState `json:"after,omitempty"`
}

// Git is wb-hostd's check of the host git (S08-08-host-git).
type Git struct {
	Binary  string `json:"binary"`
	Version string `json:"version"`
	Minimum string `json:"minimum"`
	Reason  string `json:"reason,omitempty"`
}

// Session is a session: a debug shell in V1-M2.
type Session struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	ProjectID   string `json:"project_id"`
	User        string `json:"user,omitempty"`
	Interactive bool   `json:"interactive"`
	ExitCode    *int   `json:"exit_code,omitempty"`
	Signal      *int   `json:"signal,omitempty"`
	Detail      string `json:"detail,omitempty"`
}

// Kill is a kill of a project user's processes
// (S06-10-leftover-processes).
type Kill struct {
	ID        uint64 `json:"id"`
	ProjectID string `json:"project_id"`
}

// GuestLabel is text the guest reported. Each field is escaped and cut
// by guestLabel before it is set.
type GuestLabel struct {
	ProcessesLeft string `json:"processes_left,omitempty"`
}

// maxGuestLabel bounds each guest label in bytes.
const maxGuestLabel = 256

// guestLabel is guest text as a record holds it: without the code
// points of guesttext's escaped set, and cut to maxGuestLabel bytes of
// valid UTF-8.
func guestLabel(s string) string {
	s = guesttext.Strip(s)
	if len(s) > maxGuestLabel {
		s = strings.ToValidUTF8(s[:maxGuestLabel], "")
	}
	return s
}

// The session kind of a debug shell.
const sessionDebugShell = "debug-shell"

// hostName is the host's name for a record that belongs to no VM.
var hostName = sync.OnceValue(func() string {
	name, err := os.Hostname()
	if err != nil || name == "" {
		return "localhost"
	}
	return name
})

func hostDevice() Device { return Device{TypeID: deviceUnknown, Hostname: hostName()} }

func vmDevice(slot string) Device {
	return Device{TypeID: deviceVirtual, Name: vmbundle.Name(slot)}
}

// run is a VM's run: its slot and generation.
type run struct {
	slot       string
	generation uint64
}

func (r run) app() Product {
	return Product{Name: vmbundle.Name(r.slot), UID: fmt.Sprintf("%s/%d", r.slot, r.generation)}
}

func (r run) fields(decision, rule string) Fields {
	return Fields{Decision: decision, Rule: rule, VM: r.slot, Generation: r.generation}
}

// DataDiskChanged is the 5019 record of a data disk made, deleted or
// reset (S06-13-data-disk-format, S05-03-management-commands).
func DataDiskChanged(slot, command, rule, msg string, d DataDisk) Record {
	f := Fields{Decision: "accept", Rule: rule, VM: slot, Command: command, DataDisk: &d}
	return Record{Slot: slot, Event: &ConfigChange{
		header: newHeader(configLog, severityInformational, msg, f),
		Device: vmDevice(slot),
	}}
}

// ProjectChanged is the 5019 record of a project change of `wb trust`,
// `wb untrust` or `wb project` (S09-11-audit, "Project settings";
// S05-03-management-commands). It belongs to no VM.
func ProjectChanged(command, rule string, c ProjectChange, peer *Peer) Record {
	f := Fields{Decision: "accept", Rule: rule, Command: command, Project: &c, Peer: peer}
	return Record{Event: &ConfigChange{
		header: newHeader(configLog, severityInformational, "project changed", f),
		Device: hostDevice(),
	}}
}

// GitChecked is the 5019 record of wb-hostd's host git check, accepted
// or refused (S08-08-host-git). It belongs to no VM.
func GitChecked(decision, rule string, g Git) Record {
	sev := severityInformational
	if decision != "accept" {
		sev = severityLow
	}
	f := Fields{Decision: decision, Rule: rule, Git: &g}
	return Record{Event: &ConfigChange{
		header: newHeader(configLog, sev, "host git "+decision, f),
		Device: hostDevice(),
	}}
}

// VMStarting is the 6002 record of an accepted VM start, written before
// the provider starts the VM.
func VMStarting(slot string, generation uint64, rule string, res Resources) Record {
	r := run{slot, generation}
	f := r.fields("accept", rule)
	f.Resources = &res
	return r.lifecycle(r.app(), appStart, outcomeOK, "vm start", f)
}

// VMRunning is the 6002 record of a VM the provider started.
func VMRunning(slot string, generation uint64, rule string) Record {
	r := run{slot, generation}
	return r.lifecycle(r.app(), appStart, outcomeOK, "vm running", r.fields("accept", rule))
}

// VMStopping is the 6002 record of an accepted stop: a requested one, or
// one the audit writer forced, with its cause.
func VMStopping(slot string, generation uint64, rule, cause string) Record {
	r := run{slot, generation}
	f := r.fields("accept", rule)
	f.Cause = cause
	return r.lifecycle(r.app(), appStop, outcomeOK, "vm stop", f)
}

// VMStopped is the 6002 record of a VM run's end, its stop record. A VM
// that stopped without a stop request is a failure.
func VMStopped(slot string, generation uint64, rule, cause string, unexpected bool) Record {
	r := run{slot, generation}
	f := r.fields("record", rule)
	f.Cause = cause
	o := outcomeOK
	if unexpected {
		o = outcomeFailed
	}
	rec := r.lifecycle(r.app(), appStop, o, "vm stopped", f)
	rec.Stop = true
	return rec
}

func (r run) lifecycle(app Product, a activity, o outcome, msg string, f Fields) Record {
	h := newHeader(a, o.severity, msg, f)
	h.StatusID = o.status
	return Record{Slot: r.slot, Generation: r.generation, Event: &AppLifecycle{header: h, App: app}}
}

// ShellOpened is the 6002 record of a debug shell's open
// (S06-09-session-lifecycle). The shell opens only once it is written
// (S09-11-audit, "Free space and failed writes").
func ShellOpened(slot string, generation uint64, rule string, s Session, peer *Peer) Record {
	r := run{slot, generation}
	f := r.fields("open", rule)
	f.Peer = peer
	return r.shell(appStart, outcomeOK, "shell open", s, f)
}

// ShellEnded is the 6002 record of a debug shell's end: its exit, a
// failure or its loss. decision is the relay's: end, fail or lose.
func ShellEnded(slot string, generation uint64, decision, rule string, s Session) Record {
	r := run{slot, generation}
	o := outcomeOK
	if decision != "end" {
		o = outcomeFailed
	}
	return r.shell(appStop, o, "shell "+decision, s, r.fields(decision, rule))
}

func (r run) shell(a activity, o outcome, msg string, s Session, f Fields) Record {
	s.Kind = sessionDebugShell
	f.Session = &s
	return r.lifecycle(Product{Name: sessionDebugShell, UID: s.ID}, a, o, msg, f)
}

// ProcessesLeft is the 2004 record of a kill result of processes left
// (S06-10-leftover-processes, "A result of processes left"). The count
// is the guest's, stored as its label.
func ProcessesLeft(slot string, generation uint64, rule string, k Kill, count string) Record {
	r := run{slot, generation}
	f := r.fields("refuse-vm", rule)
	f.Kill = &k
	f.GuestLabel = &GuestLabel{ProcessesLeft: guestLabel(count)}
	return r.finding(severityMedium, "processes left in the VM", k, f)
}

// KillClosed is the 2004 record of a pending kill that a cold stop
// closed (S06-10-leftover-processes, "Failing closed").
func KillClosed(slot string, generation uint64, rule string, k Kill, cause string) Record {
	r := run{slot, generation}
	f := r.fields("close", rule)
	f.Kill = &k
	f.Cause = cause
	return r.finding(severityLow, "kill closed", k, f)
}

func (r run) finding(sev int, title string, k Kill, f Fields) Record {
	return Record{Slot: r.slot, Generation: r.generation, Event: &Finding{
		header: newHeader(findingCreate, sev, title, f),
		FindingInfo: FindingInfo{
			UID: fmt.Sprintf("%s/%d/kill/%d", r.slot, r.generation, k.ID), Title: title, Types: []string{f.Rule},
		},
	}}
}

// WriteFailed is the 2004 record of the writer's own failure, which it
// queues when writes start failing, behind the records that failed. Its
// time is when the gap in the log began, and writing it ends the
// refusal. It belongs to no VM.
func WriteFailed(since time.Time, cause string) Record {
	f := Fields{Decision: "stop-vm", Rule: RuleWriteFailed, Cause: cause}
	const title = "audit write failed"
	return Record{Event: &Finding{
		header: newHeader(findingCreate, severityHigh, title, f),
		FindingInfo: FindingInfo{
			UID: fmt.Sprintf("audit/%s/%d", RuleWriteFailed, since.UnixMilli()), Title: title, Types: []string{RuleWriteFailed},
		},
	}}
}
