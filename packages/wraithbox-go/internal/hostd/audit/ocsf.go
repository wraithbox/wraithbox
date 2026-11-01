package audit

// The OCSF 1.8.0 classes the writer writes (S09-11-audit, "Classes";
// X24-openshell-artifacts). Each has one Go type, with the OCSF
// attribute names. Network Activity (4001) and HTTP Activity (4002)
// come on the event channel, and their types are in channel.go.
const (
	OCSFVersion = "1.8.0"

	ClassDeviceConfigStateChange = 5019
	ClassDetectionFinding        = 2004
	ClassApplicationLifecycle    = 6002
	ClassNetworkActivity         = 4001
	ClassHTTPActivity            = 4002

	categoryFindings    = 2
	categoryNetwork     = 4
	categoryDiscovery   = 5
	categoryApplication = 6
)

// activity is an activity of a class: its class, category, OCSF
// activity_id and activity_name.
type activity struct {
	class, category, id int
	name                string
}

// The activities the writer records.
var (
	configLog     = activity{ClassDeviceConfigStateChange, categoryDiscovery, 1, "Log"}
	appStart      = activity{ClassApplicationLifecycle, categoryApplication, 3, "Start"}
	appStop       = activity{ClassApplicationLifecycle, categoryApplication, 4, "Stop"}
	findingCreate = activity{ClassDetectionFinding, categoryFindings, 1, "Create"}
)

// outcome is a record's OCSF severity_id and status_id.
type outcome struct{ severity, status int }

var (
	outcomeOK     = outcome{severityInformational, statusSuccess}
	outcomeFailed = outcome{severityLow, statusFailure}
)

// Severities, by their OCSF severity_id.
const (
	severityInformational = 1
	severityLow           = 2
	severityMedium        = 3
	severityHigh          = 4
)

// Statuses, by their OCSF status_id.
const (
	statusSuccess = 1
	statusFailure = 2
)

// Device types, by their OCSF type_id.
const (
	deviceUnknown = 0
	deviceVirtual = 6
)

// productName names Wraith Box as the product of every record.
const productName = "Wraith Box"

// Product is an OCSF product object: Wraith Box in metadata, and the
// application of an Application Lifecycle record.
type Product struct {
	Name       string `json:"name"`
	VendorName string `json:"vendor_name,omitempty"`
	Version    string `json:"version,omitempty"`
	UID        string `json:"uid,omitempty"`
}

// Metadata is the OCSF metadata object.
type Metadata struct {
	Product Product `json:"product"`
	Version string  `json:"version"`
	LogName string  `json:"log_name"`
}

// Device is the OCSF device object: the VM, or the host for a record
// that belongs to none.
type Device struct {
	TypeID   int    `json:"type_id"`
	Name     string `json:"name,omitempty"`
	Hostname string `json:"hostname,omitempty"`
	UID      string `json:"uid,omitempty"`
}

// FindingInfo is the OCSF finding_info object.
type FindingInfo struct {
	UID   string   `json:"uid"`
	Title string   `json:"title"`
	Desc  string   `json:"desc,omitempty"`
	Types []string `json:"types,omitempty"`
}

// header holds the attributes every class has. The writer sets Time and
// Metadata when it accepts the record.
type header struct {
	ActivityID   int      `json:"activity_id"`
	ActivityName string   `json:"activity_name"`
	CategoryUID  int      `json:"category_uid"`
	ClassUID     int      `json:"class_uid"`
	TypeUID      int      `json:"type_uid"`
	SeverityID   int      `json:"severity_id"`
	StatusID     int      `json:"status_id,omitempty"`
	Time         int64    `json:"time"`
	Message      string   `json:"message"`
	Metadata     Metadata `json:"metadata"`
	Unmapped     Fields   `json:"unmapped"`
}

func (h *header) head() *header { return h }

func newHeader(a activity, severity int, msg string, f Fields) header {
	return header{
		ActivityID: a.id, ActivityName: a.name, CategoryUID: a.category, ClassUID: a.class,
		TypeUID: a.class*100 + a.id, SeverityID: severity, Message: msg, Unmapped: f,
	}
}

// ConfigChange is a Device Config State Change (5019) record.
type ConfigChange struct {
	header
	Device Device `json:"device"`
}

// AppLifecycle is an Application Lifecycle (6002) record: a VM's start
// or stop, or a session's open or end.
type AppLifecycle struct {
	header
	App Product `json:"app"`
}

// Finding is a Detection Finding (2004) record. Device is the VM of a
// finding that came on the event channel.
type Finding struct {
	header
	Device      *Device     `json:"device,omitempty"`
	FindingInfo FindingInfo `json:"finding_info"`
}

// Event is a record of one of the classes above.
type Event interface {
	head() *header
}

var (
	_ Event = (*ConfigChange)(nil)
	_ Event = (*AppLifecycle)(nil)
	_ Event = (*Finding)(nil)
)
