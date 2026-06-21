package httpx

import (
	"fmt"
	"net/http"
)

// checkRedirect is the http.Client.CheckRedirect hook. It bounds the redirect
// chain and refuses anything that weakens transport security: non-http(s)
// targets and https->http downgrades. http->https upgrades and same-scheme
// redirects are allowed. via is never empty when this is called.
func (c *Client) checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= c.maxRedirects {
		return ErrTooManyRedirects
	}
	switch req.URL.Scheme {
	case "https":
		// Always safe: same-scheme or an http->https upgrade.
	case "http":
		if via[len(via)-1].URL.Scheme == "https" {
			return fmt.Errorf("%w: refusing https->http downgrade", ErrInsecureRedirect)
		}
	default:
		return fmt.Errorf("%w: scheme %q", ErrInsecureRedirect, req.URL.Scheme)
	}
	return nil
}
