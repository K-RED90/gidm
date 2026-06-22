package apiserver

import (
	"context"
	"errors"
	"io"

	"github.com/K-RED90/gidm/api"
	"github.com/K-RED90/gidm/internal/engine"
)

// dispatch validates the request envelope and routes the verb to the matching
// Manager call, propagating ctx (the server's lifecycle context) so a shutdown
// cancels in-flight Manager work. It returns the response plus the verb and id
// used for structured logging. Internal errors are mapped to a generic response
// by toResponse; the full error is logged there, never sent to the client.
func (s *Server) dispatch(ctx context.Context, req *api.Request) (resp api.Response, verb, id string) {
	verb = string(req.Op)

	if req.Version != api.Version {
		return api.ErrorResponse(api.CodeUnsupportedVersion, "unsupported protocol version %d", req.Version), verb, ""
	}

	switch req.Op {
	case api.OpAdd:
		if req.Add == nil {
			return api.ErrorResponse(api.CodeBadRequest, "missing add payload"), verb, ""
		}
		if err := api.ValidateAdd(*req.Add); err != nil {
			return s.toResponse(err), verb, ""
		}
		newID, err := s.mgr.Submit(ctx, req.Add.URL, priorityFromView(req.Add.Priority, s.mgr.Settings().DefaultPriority), engine.AddOptions{
			Dir:      req.Add.Dir,
			Filename: req.Add.Filename,
			Segments: req.Add.Segments,
		})
		if err != nil {
			return s.toResponse(err), verb, ""
		}
		return api.AddResponse(newID), verb, newID

	case api.OpList:
		list, err := s.mgr.List(ctx)
		if err != nil {
			return s.toResponse(err), verb, ""
		}
		views := make([]api.DownloadView, 0, len(list))
		for _, d := range list {
			views = append(views, toView(d, false))
		}
		return api.ListResponse(views), verb, ""

	case api.OpStatus:
		if req.Status == nil {
			return api.ErrorResponse(api.CodeBadRequest, "missing status payload"), verb, ""
		}
		if err := api.ValidateID(req.Status.ID); err != nil {
			return s.toResponse(err), verb, ""
		}
		id = req.Status.ID
		d, err := s.mgr.Get(ctx, id)
		if err != nil {
			return s.toResponse(err), verb, id
		}
		return api.StatusResponse(toView(d, true)), verb, id

	case api.OpPause:
		if req.Pause == nil {
			return api.ErrorResponse(api.CodeBadRequest, "missing pause payload"), verb, ""
		}
		if err := api.ValidateID(req.Pause.ID); err != nil {
			return s.toResponse(err), verb, ""
		}
		id = req.Pause.ID
		if err := s.mgr.Pause(ctx, id); err != nil {
			return s.toResponse(err), verb, id
		}
		return api.OKResponse(), verb, id

	case api.OpResume:
		if req.Resume == nil {
			return api.ErrorResponse(api.CodeBadRequest, "missing resume payload"), verb, ""
		}
		if err := api.ValidateID(req.Resume.ID); err != nil {
			return s.toResponse(err), verb, ""
		}
		id = req.Resume.ID
		if err := s.mgr.Resume(ctx, id); err != nil {
			return s.toResponse(err), verb, id
		}
		return api.OKResponse(), verb, id

	case api.OpRm:
		if req.Rm == nil {
			return api.ErrorResponse(api.CodeBadRequest, "missing rm payload"), verb, ""
		}
		if err := api.ValidateID(req.Rm.ID); err != nil {
			return s.toResponse(err), verb, ""
		}
		id = req.Rm.ID
		// rm deletes the download: it is removed from the store and its .part file is
		// discarded (a completed download's final file is left in place). To merely
		// stop a transfer while keeping it for a later Resume, use pause.
		if err := s.mgr.Delete(ctx, id); err != nil {
			return s.toResponse(err), verb, id
		}
		return api.OKResponse(), verb, id

	case api.OpSetPriority:
		if req.SetPriority == nil {
			return api.ErrorResponse(api.CodeBadRequest, "missing set_priority payload"), verb, ""
		}
		if err := api.ValidateSetPriority(*req.SetPriority); err != nil {
			return s.toResponse(err), verb, ""
		}
		id = req.SetPriority.ID
		if err := s.mgr.SetPriority(ctx, id, priorityFromView(req.SetPriority.Priority, s.mgr.Settings().DefaultPriority)); err != nil {
			return s.toResponse(err), verb, id
		}
		return api.OKResponse(), verb, id

	case api.OpSetRate:
		if req.SetRate == nil {
			return api.ErrorResponse(api.CodeBadRequest, "missing set_rate payload"), verb, ""
		}
		if err := api.ValidateSetRate(*req.SetRate); err != nil {
			return s.toResponse(err), verb, ""
		}
		id = req.SetRate.ID
		if err := s.mgr.SetRate(ctx, id, req.SetRate.MaxRate); err != nil {
			return s.toResponse(err), verb, id
		}
		return api.OKResponse(), verb, id

	case api.OpGetConfig:
		return api.ConfigResponse(toConfigView(s.mgr.Settings())), verb, ""

	case api.OpSetConfig:
		if req.SetConfig == nil {
			return api.ErrorResponse(api.CodeBadRequest, "missing set_config payload"), verb, ""
		}
		if err := api.ValidateSetConfig(*req.SetConfig); err != nil {
			return s.toResponse(err), verb, ""
		}
		// Patch the requested fields onto the current settings (nil fields unchanged),
		// apply+persist, then echo the now-current settings so the client sees the
		// effective result.
		merged := applyConfigPatch(s.mgr.Settings(), *req.SetConfig)
		if err := s.mgr.SetSettings(ctx, merged); err != nil {
			return s.toResponse(err), verb, ""
		}
		return api.ConfigResponse(toConfigView(s.mgr.Settings())), verb, ""

	case api.OpPing:
		// Health/version only — no Manager call. This also answers the
		// single-instance liveness probe a starting daemon sends.
		return api.PingResponse(), verb, ""

	default:
		return api.ErrorResponse(api.CodeBadRequest, "unknown op %q", req.Op), verb, ""
	}
}

// toResponse maps an internal error to a stable wire response. Known sentinels
// map to specific codes; everything else is logged in full at error level and
// answered with a generic "internal error" so no engine error string or
// filesystem path ever leaks to the client.
func (s *Server) toResponse(err error) api.Response {
	switch {
	case errors.Is(err, engine.ErrNotFound):
		return api.ErrorResponse(api.CodeNotFound, "download not found")
	case errors.Is(err, engine.ErrNotResumable):
		return api.ErrorResponse(api.CodeBadRequest, "download is not resumable")
	case errors.Is(err, api.ErrInvalidURL):
		return api.ErrorResponse(api.CodeBadRequest, "url must be an absolute http or https URL")
	case errors.Is(err, api.ErrEmptyID):
		return api.ErrorResponse(api.CodeBadRequest, "id must be non-empty")
	case errors.Is(err, api.ErrInvalidPriority):
		return api.ErrorResponse(api.CodeBadRequest, "priority must be low, normal, or high")
	case errors.Is(err, api.ErrInvalidDestination):
		return api.ErrorResponse(api.CodeBadRequest, "dir must be an absolute path and filename a single name")
	case errors.Is(err, api.ErrInvalidSegments):
		return api.ErrorResponse(api.CodeBadRequest, "segments must be between 0 and 64")
	case errors.Is(err, api.ErrInvalidRate):
		return api.ErrorResponse(api.CodeBadRequest, "rate must be a non-negative number of bytes per second")
	default:
		// ErrManagerClosed and any unanticipated failure fall here: log the detail,
		// return a generic message.
		s.logger.Error("apiserver: request failed", "err", err)
		return api.ErrorResponse(api.CodeInternal, "internal error")
	}
}

// newLimitedReader caps a request frame at max bytes. Reading past the cap
// surfaces as a decode error (EOF mid-object), which the handler answers with a
// bad_request — an oversized payload cannot exhaust memory.
func newLimitedReader(r io.Reader, max int64) io.Reader {
	return io.LimitReader(r, max)
}
