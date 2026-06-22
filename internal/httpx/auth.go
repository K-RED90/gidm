package httpx

import "net/http"

// RequestOptions carries per-request authentication applied to every attempt of a
// Probe/RangeGet/Get: HTTP Basic auth, a Referer, an explicit Cookie, and extra
// request headers. The zero value adds nothing, so an unauthenticated request is
// unchanged.
type RequestOptions struct {
	Username string
	Password string
	Referer  string
	Cookie   string
	Headers  map[string]string
}

// apply sets the options onto req. Callers invoke it inside the per-attempt setup
// callback, before the engine-managed Range header, so the credentials ride every
// retry. The standard client strips Authorization and Cookie on a cross-host
// redirect, so credentials never leak to a different origin (the CDN a download
// page redirects to) — the desired per-host behavior. Custom headers are set last
// but the wire boundary already rejects framing/auth-critical names, so they
// cannot clobber Range, Host, or the Authorization set here.
func (o RequestOptions) apply(req *http.Request) {
	if o.Username != "" || o.Password != "" {
		req.SetBasicAuth(o.Username, o.Password)
	}
	if o.Referer != "" {
		req.Header.Set("Referer", o.Referer)
	}
	if o.Cookie != "" {
		req.Header.Set("Cookie", o.Cookie)
	}
	for k, v := range o.Headers {
		req.Header.Set(k, v)
	}
}
