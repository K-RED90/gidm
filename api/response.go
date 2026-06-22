package api

import "fmt"

// Code is a stable, machine-readable error code. The set is closed and never
// leaks internal/engine identifiers: Code is for programmatic handling, the
// human-readable Message is for display.
type Code string

const (
	CodeNotFound           Code = "not_found"
	CodeBadRequest         Code = "bad_request"
	CodeInternal           Code = "internal"
	CodeUnsupportedVersion Code = "unsupported_version"
)

// Error is the structured error carried by a failed Response.
type Error struct {
	Code    Code   `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Message) }

// Response is the wire response envelope. OK is the discriminator: when true,
// Error is nil and the result pointer matching the request verb is set (pause,
// resume, and rm are bare acks with no result body); when false, Error is
// non-nil and all result pointers are nil.
type Response struct {
	Version int    `json:"version"`
	OK      bool   `json:"ok"`
	Error   *Error `json:"error,omitempty"`

	Add    *AddResult    `json:"add,omitempty"`
	List   *ListResult   `json:"list,omitempty"`
	Status *StatusResult `json:"status,omitempty"`
	Ping   *PingResult   `json:"ping,omitempty"`
}

// OKResponse is a bare success acknowledgement (used by pause, resume, rm).
func OKResponse() Response {
	return Response{Version: Version, OK: true}
}

// ErrorResponse builds a failed response with a stable Code and a formatted
// human message.
func ErrorResponse(code Code, format string, args ...any) Response {
	return Response{
		Version: Version,
		OK:      false,
		Error:   &Error{Code: code, Message: fmt.Sprintf(format, args...)},
	}
}

func AddResponse(id string) Response {
	return Response{Version: Version, OK: true, Add: &AddResult{ID: id}}
}

func ListResponse(downloads []DownloadView) Response {
	return Response{Version: Version, OK: true, List: &ListResult{Downloads: downloads}}
}

func StatusResponse(d DownloadView) Response {
	return Response{Version: Version, OK: true, Status: &StatusResult{Download: d}}
}

func PingResponse() Response {
	return Response{Version: Version, OK: true, Ping: &PingResult{Version: Version}}
}
