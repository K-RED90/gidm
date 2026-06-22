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
	OpRestart     Op = "restart" // discard progress and re-download from scratch
	OpRm          Op = "rm"
	OpSetPriority Op = "set-priority"
	OpSetRate     Op = "set-rate"   // per-download bandwidth cap
	OpGetConfig   Op = "get-config" // read daemon runtime settings
	OpSetConfig   Op = "set-config" // change daemon runtime settings
	OpPing        Op = "ping"       // health check
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
	Restart     *Restart     `json:"restart,omitempty"`
	Rm          *Rm          `json:"rm,omitempty"`
	SetPriority *SetPriority `json:"set_priority,omitempty"`
	SetRate     *SetRate     `json:"set_rate,omitempty"`
	SetConfig   *SetConfig   `json:"set_config,omitempty"`
}

type Add struct {
	URL string `json:"url"`
	// Priority is optional; an empty value means PriorityNormal, so an older
	// client that omits the field still produces a valid normal-priority add.
	Priority Priority `json:"priority,omitempty"`

	// Dir, Filename, and Segments are optional per-download overrides. Each zero
	// value means "use the daemon default", so an older client (or the bare-URL
	// form) that omits them is unchanged: Dir empty → the configured download
	// directory; Filename empty → the server-suggested or URL-derived name;
	// Segments 0 → cfg.SegmentsPerDownload.
	Dir      string `json:"dir,omitempty"`      // destination directory (absolute)
	Filename string `json:"filename,omitempty"` // single path element, no separators
	Segments int    `json:"segments,omitempty"` // per-download segment count
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

// Restart re-downloads a download from the beginning, discarding its partial
// progress (checkpoints and the .part file). Distinct from Resume, which
// continues from the last checkpoint.
type Restart struct {
	ID string `json:"id"`
}

type Rm struct {
	ID string `json:"id"`
}

type SetPriority struct {
	ID       string   `json:"id"`
	Priority Priority `json:"priority"`
}

// SetRate sets one download's bandwidth cap in bytes/sec. Zero removes the
// per-download cap, so the download inherits the daemon's default.
type SetRate struct {
	ID      string `json:"id"`
	MaxRate int    `json:"max_rate"`
}

// SetConfig is a partial update of the daemon's runtime settings: a nil field is
// left unchanged. Rates are bytes/sec with 0 = unlimited, so 0 is a real value —
// which is why the fields are pointers rather than using the zero value as "unset".
type SetConfig struct {
	DownloadDir         *string   `json:"download_dir,omitempty"`
	SegmentsPerDownload *int      `json:"segments_per_download,omitempty"`
	DefaultPriority     *Priority `json:"default_priority,omitempty"`
	MaxRate             *int      `json:"max_rate,omitempty"`
	PerDownloadMaxRate  *int      `json:"per_download_max_rate,omitempty"`
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

// NewAddRequestWithOptions builds an add request carrying the full set of
// per-download overrides. The URL is set from url; opts supplies the optional
// Priority/Dir/Filename/Segments (any opts.URL is ignored in favor of url).
func NewAddRequestWithOptions(url string, opts Add) Request {
	opts.URL = url
	return Request{Version: Version, Op: OpAdd, Add: &opts}
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

func NewRestartRequest(id string) Request {
	return Request{Version: Version, Op: OpRestart, Restart: &Restart{ID: id}}
}

func NewRmRequest(id string) Request {
	return Request{Version: Version, Op: OpRm, Rm: &Rm{ID: id}}
}

func NewSetPriorityRequest(id string, p Priority) Request {
	return Request{Version: Version, Op: OpSetPriority, SetPriority: &SetPriority{ID: id, Priority: p}}
}

func NewSetRateRequest(id string, maxRate int) Request {
	return Request{Version: Version, Op: OpSetRate, SetRate: &SetRate{ID: id, MaxRate: maxRate}}
}

func NewGetConfigRequest() Request {
	return Request{Version: Version, Op: OpGetConfig}
}

func NewSetConfigRequest(sc SetConfig) Request {
	return Request{Version: Version, Op: OpSetConfig, SetConfig: &sc}
}

func NewPingRequest() Request {
	return Request{Version: Version, Op: OpPing}
}
