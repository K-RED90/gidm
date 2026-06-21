package main

import (
	"encoding/json"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/K-RED90/gidm/internal/api"
)

// writeJSON marshals v as a single compact line plus a trailing newline, so the
// output is one JSON object per invocation and pipeline-friendly.
func writeJSON(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("render: marshal json: %w", err)
	}
	if _, err := w.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("render: write json: %w", err)
	}
	return nil
}

// renderAdd prints the new download id (human) or the AddResult (json).
func renderAdd(w io.Writer, resp api.Response, asJSON bool) error {
	if asJSON {
		return writeJSON(w, resp.Add)
	}
	if _, err := fmt.Fprintln(w, resp.Add.ID); err != nil {
		return fmt.Errorf("render: write id: %w", err)
	}
	return nil
}

// renderList prints a tab-aligned table (human) or the ListResult (json). Empty
// lists print a short notice rather than a bare header.
func renderList(w io.Writer, resp api.Response, asJSON bool) error {
	if asJSON {
		return writeJSON(w, resp.List)
	}
	if len(resp.List.Downloads) == 0 {
		if _, err := fmt.Fprintln(w, "no downloads"); err != nil {
			return fmt.Errorf("render: write empty list: %w", err)
		}
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "ID\tSTATUS\tPROGRESS\tSIZE\tDESTINATION")
	for _, d := range resp.List.Downloads {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
			d.ID, d.Status, progress(d), humanBytes(d.TotalSize), destination(d))
	}
	if err := tw.Flush(); err != nil {
		return fmt.Errorf("render: flush table: %w", err)
	}
	return nil
}

// renderStatus prints one download in the same columns as renderList (human) or
// the StatusResult (json).
func renderStatus(w io.Writer, resp api.Response, asJSON bool) error {
	if asJSON {
		return writeJSON(w, resp.Status)
	}
	d := resp.Status.Download
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	_, _ = fmt.Fprintf(tw, "ID:\t%s\n", d.ID)
	_, _ = fmt.Fprintf(tw, "URL:\t%s\n", d.URL)
	_, _ = fmt.Fprintf(tw, "Status:\t%s\n", d.Status)
	_, _ = fmt.Fprintf(tw, "Progress:\t%s\n", progress(d))
	_, _ = fmt.Fprintf(tw, "Size:\t%s\n", humanBytes(d.TotalSize))
	_, _ = fmt.Fprintf(tw, "Destination:\t%s\n", destination(d))
	if err := tw.Flush(); err != nil {
		return fmt.Errorf("render: flush status: %w", err)
	}
	return nil
}

// renderAck prints a short confirmation for pause/resume/rm (human) or the bare
// ok response (json). The verb-derived word and the id come from the request,
// since the ack response carries no body.
func renderAck(w io.Writer, resp api.Response, word, id string, asJSON bool) error {
	if asJSON {
		return writeJSON(w, map[string]bool{"ok": resp.OK})
	}
	if _, err := fmt.Fprintf(w, "%s %s\n", word, id); err != nil {
		return fmt.Errorf("render: write ack: %w", err)
	}
	return nil
}

// progress renders downloaded/total plus a percentage, guarding TotalSize == 0
// (unknown length) against a divide-by-zero.
func progress(d api.DownloadView) string {
	if d.TotalSize <= 0 {
		return fmt.Sprintf("%s/unknown", humanBytes(d.Downloaded))
	}
	pct := float64(d.Downloaded) / float64(d.TotalSize) * 100
	return fmt.Sprintf("%s/%s (%.1f%%)", humanBytes(d.Downloaded), humanBytes(d.TotalSize), pct)
}

func destination(d api.DownloadView) string {
	if d.Destination == "" {
		return "-"
	}
	return d.Destination
}

// humanBytes formats a byte count with a binary unit suffix. Cold-path UI, so it
// allocates freely.
func humanBytes(n int64) string {
	if n < 0 {
		return "-"
	}
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
