package api

// Version is the wire protocol version. It is bumped only on an incompatible
// change; a server that receives a Version it cannot serve replies with
// CodeUnsupportedVersion.
const Version = 1

// Op is the request verb. The set is closed: a peer that sends an Op outside
// these constants is answered with CodeBadRequest.
type Op string

const (
	OpAdd         Op = "add"
	OpList        Op = "list"
	OpStatus      Op = "status"
	OpPause       Op = "pause"
	OpResume      Op = "resume"
	OpRm          Op = "rm"
	OpSetPriority Op = "set-priority"
	OpPing        Op = "ping" // health check
)

// Request is the envelope-with-op wire request. Op selects the verb; the
// matching payload pointer is set and the rest are nil. List and Ping carry no
// payload. The server validates that the payload for Op is present — JSON shape
// alone does not guarantee it.
type Request struct {
	Version int `json:"version"`
	Op      Op  `json:"op"`

	Add         *Add         `json:"add,omitempty"`
	Status      *Status      `json:"status,omitempty"`
	Pause       *Pause       `json:"pause,omitempty"`
	Resume      *Resume      `json:"resume,omitempty"`
	Rm          *Rm          `json:"rm,omitempty"`
	SetPriority *SetPriority `json:"set_priority,omitempty"`
}

type Add struct {
	URL string `json:"url"`
	// Priority is optional; an empty value means PriorityNormal, so an older
	// client that omits the field still produces a valid normal-priority add.
	Priority Priority `json:"priority,omitempty"`
}

type Status struct {
	ID string `json:"id"`
}

type Pause struct {
	ID string `json:"id"`
}

type Resume struct {
	ID string `json:"id"`
}

type Rm struct {
	ID string `json:"id"`
}

type SetPriority struct {
	ID       string   `json:"id"`
	Priority Priority `json:"priority"`
}

// NewAddRequest builds a well-formed add request at normal priority, stamping
// Version and Op so callers and tests construct envelopes one way.
func NewAddRequest(url string) Request {
	return Request{Version: Version, Op: OpAdd, Add: &Add{URL: url}}
}

// NewAddRequestWithPriority is NewAddRequest with an explicit priority.
func NewAddRequestWithPriority(url string, p Priority) Request {
	return Request{Version: Version, Op: OpAdd, Add: &Add{URL: url, Priority: p}}
}

func NewListRequest() Request {
	return Request{Version: Version, Op: OpList}
}

func NewStatusRequest(id string) Request {
	return Request{Version: Version, Op: OpStatus, Status: &Status{ID: id}}
}

func NewPauseRequest(id string) Request {
	return Request{Version: Version, Op: OpPause, Pause: &Pause{ID: id}}
}

func NewResumeRequest(id string) Request {
	return Request{Version: Version, Op: OpResume, Resume: &Resume{ID: id}}
}

func NewRmRequest(id string) Request {
	return Request{Version: Version, Op: OpRm, Rm: &Rm{ID: id}}
}

func NewSetPriorityRequest(id string, p Priority) Request {
	return Request{Version: Version, Op: OpSetPriority, SetPriority: &SetPriority{ID: id, Priority: p}}
}

func NewPingRequest() Request {
	return Request{Version: Version, Op: OpPing}
}
