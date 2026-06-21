package api

// Version is the wire protocol version. It is bumped only on an incompatible
// change; a server that receives a Version it cannot serve replies with
// CodeUnsupportedVersion.
const Version = 1

// Op is the request verb. The set is closed: a peer that sends an Op outside
// these constants is answered with CodeBadRequest.
type Op string

const (
	OpAdd    Op = "add"
	OpList   Op = "list"
	OpStatus Op = "status"
	OpPause  Op = "pause"
	OpResume Op = "resume"
	OpRm     Op = "rm"
	OpPing   Op = "ping" // health check
)

// Request is the envelope-with-op wire request. Op selects the verb; the
// matching payload pointer is set and the rest are nil. List and Ping carry no
// payload. The server validates that the payload for Op is present — JSON shape
// alone does not guarantee it.
type Request struct {
	Version int `json:"version"`
	Op      Op  `json:"op"`

	Add    *Add    `json:"add,omitempty"`
	Status *Status `json:"status,omitempty"`
	Pause  *Pause  `json:"pause,omitempty"`
	Resume *Resume `json:"resume,omitempty"`
	Rm     *Rm     `json:"rm,omitempty"`
}

type Add struct {
	URL string `json:"url"`
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

// NewAddRequest builds a well-formed add request, stamping Version and Op so
// callers and tests construct envelopes one way.
func NewAddRequest(url string) Request {
	return Request{Version: Version, Op: OpAdd, Add: &Add{URL: url}}
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

func NewPingRequest() Request {
	return Request{Version: Version, Op: OpPing}
}
