package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/K-RED90/gidm/api"
	"github.com/K-RED90/gidm/internal/config"
)

var version = "0.1.0-dev"

// Exit codes. 0 is success; every failure is non-zero, and the mapping is kept
// small and stable so scripts can branch on it: 1 generic/internal, 2 bad
// request/usage, 3 daemon not running, 4 timeout, 5 not found.
const (
	exitOK          = 0
	exitGeneric     = 1
	exitBadRequest  = 2
	exitDaemonNotUp = 3
	exitTimeout     = 4
	exitNotFound    = 5
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// Run is an exported, stdlib-only wrapper around run so an external test package
// can drive the CLI through its real entry seam without spawning a process. It
// references only run and therefore does not widen cmd/gidm's import graph.
func Run(args []string, stdout, stderr io.Writer) int {
	return run(args, stdout, stderr)
}

// run is the testable entry point: it parses global flags, layers them over the
// loaded config, dispatches the subcommand, and returns a process exit code. All
// output goes to the provided writers (never os.Stdout/os.Stderr directly) so
// tests can capture it.
func run(args []string, stdout, stderr io.Writer) int {
	gf := flag.NewFlagSet("gidm", flag.ContinueOnError)
	gf.SetOutput(stderr)
	var (
		socket      = gf.String("socket", "", "unix socket path override")
		cfgPath     = gf.String("config", "", "config file path (overrides $GIDM_CONFIG)")
		asJSON      = gf.Bool("json", false, "emit the raw api response as JSON")
		timeout     = gf.Duration("timeout", 0, "dial/request timeout override (e.g. 5s)")
		showVersion = gf.Bool("version", false, "print version and exit")
	)
	gf.Usage = func() { usage(stderr, gf) }
	if err := gf.Parse(args); err != nil {
		// flag already printed the error and usage to stderr.
		return exitBadRequest
	}

	if *showVersion {
		_, _ = fmt.Fprintln(stdout, version)
		return exitOK
	}

	rest := gf.Args()
	if len(rest) == 0 {
		usage(stderr, gf)
		return exitBadRequest
	}

	cfg, err := loadConfig(*cfgPath)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "gidm:", err)
		return exitGeneric
	}
	// Flags are the highest-priority config layer.
	if *socket != "" {
		cfg.Daemon.SocketPath = *socket
	}
	if *timeout > 0 {
		cfg.Daemon.DialTimeout = config.Duration(*timeout)
	}
	if err := cfg.Validate(); err != nil {
		_, _ = fmt.Fprintln(stderr, "gidm:", err)
		return exitGeneric
	}

	c := &client{socket: cfg.Daemon.SocketPath, timeout: cfg.Daemon.DialTimeout.Duration()}
	return dispatch(rest, c, *asJSON, stdout, stderr)
}

func loadConfig(path string) (*config.Config, error) {
	if path != "" {
		return config.LoadFrom(path)
	}
	return config.Load()
}

// dispatch selects and runs the subcommand named by args[0]. Each subcommand
// owns its own flag.FlagSet (parsed over the remaining args) and validates its
// arity before building the api request. "get" is an alias of "status".
func dispatch(args []string, c *client, asJSON bool, stdout, stderr io.Writer) int {
	cmd := args[0]
	rest := args[1:]
	switch cmd {
	case "add":
		fs := flag.NewFlagSet("add", flag.ContinueOnError)
		fs.SetOutput(stderr)
		priority := fs.String("priority", "", "download priority: low | normal | high (default normal)")
		dir := fs.String("dir", "", "destination directory (absolute; default: configured download dir)")
		filename := fs.String("filename", "", "output filename (single name, no path separators)")
		segments := fs.Int("segments", 0, "number of parallel segments (0 = daemon default)")
		if err := fs.Parse(rest); err != nil {
			return exitBadRequest
		}
		if fs.NArg() != 1 {
			_, _ = fmt.Fprintln(stderr, "gidm: add requires exactly one argument: add [flags] <url>")
			return exitBadRequest
		}
		add := api.Add{
			URL:      fs.Arg(0),
			Priority: api.Priority(*priority),
			Dir:      *dir,
			Filename: *filename,
			Segments: *segments,
		}
		// Validate client-side for a clear, immediate message; the daemon revalidates.
		if err := api.ValidateAdd(add); err != nil {
			_, _ = fmt.Fprintln(stderr, "gidm:", err)
			return exitBadRequest
		}
		return runCommand(c, asJSON, stdout, stderr, api.NewAddRequestWithOptions(add.URL, add),
			func(w io.Writer, r api.Response) error { return renderAdd(w, r, asJSON) })
	case "list":
		if code := noArgs(stderr, "list", rest); code != exitOK {
			return code
		}
		return runCommand(c, asJSON, stdout, stderr, api.NewListRequest(),
			func(w io.Writer, r api.Response) error { return renderList(w, r, asJSON) })
	case "status", "get":
		id, code := oneArg(stderr, cmd, "<id>", rest)
		if code != exitOK {
			return code
		}
		return runCommand(c, asJSON, stdout, stderr, api.NewStatusRequest(id),
			func(w io.Writer, r api.Response) error { return renderStatus(w, r, asJSON) })
	case "pause":
		id, code := oneArg(stderr, "pause", "<id>", rest)
		if code != exitOK {
			return code
		}
		return runCommand(c, asJSON, stdout, stderr, api.NewPauseRequest(id),
			func(w io.Writer, r api.Response) error { return renderAck(w, r, "paused", id, asJSON) })
	case "resume":
		id, code := oneArg(stderr, "resume", "<id>", rest)
		if code != exitOK {
			return code
		}
		return runCommand(c, asJSON, stdout, stderr, api.NewResumeRequest(id),
			func(w io.Writer, r api.Response) error { return renderAck(w, r, "resumed", id, asJSON) })
	case "restart":
		id, code := oneArg(stderr, "restart", "<id>", rest)
		if code != exitOK {
			return code
		}
		return runCommand(c, asJSON, stdout, stderr, api.NewRestartRequest(id),
			func(w io.Writer, r api.Response) error { return renderAck(w, r, "restarting", id, asJSON) })
	case "rm":
		id, code := oneArg(stderr, "rm", "<id>", rest)
		if code != exitOK {
			return code
		}
		return runCommand(c, asJSON, stdout, stderr, api.NewRmRequest(id),
			func(w io.Writer, r api.Response) error { return renderAck(w, r, "removed", id, asJSON) })
	case "set-priority":
		id, level, code := twoArgs(stderr, "set-priority", "<id> <low|normal|high>", rest)
		if code != exitOK {
			return code
		}
		return runCommand(c, asJSON, stdout, stderr, api.NewSetPriorityRequest(id, api.Priority(level)),
			func(w io.Writer, r api.Response) error { return renderAck(w, r, "priority set", id, asJSON) })
	case "set-rate":
		fs := flag.NewFlagSet("set-rate", flag.ContinueOnError)
		fs.SetOutput(stderr)
		maxRate := fs.Int("max-rate", 0, "per-download cap in bytes/sec (0 removes the cap)")
		if err := fs.Parse(rest); err != nil {
			return exitBadRequest
		}
		if fs.NArg() != 1 {
			_, _ = fmt.Fprintln(stderr, "gidm: set-rate requires exactly one argument: set-rate [--max-rate=N] <id>")
			return exitBadRequest
		}
		id := fs.Arg(0)
		if err := api.ValidateSetRate(api.SetRate{ID: id, MaxRate: *maxRate}); err != nil {
			_, _ = fmt.Fprintln(stderr, "gidm:", err)
			return exitBadRequest
		}
		return runCommand(c, asJSON, stdout, stderr, api.NewSetRateRequest(id, *maxRate),
			func(w io.Writer, r api.Response) error { return renderAck(w, r, "rate set", id, asJSON) })
	case "config":
		return dispatchConfig(rest, c, asJSON, stdout, stderr)
	default:
		_, _ = fmt.Fprintf(stderr, "gidm: unknown command %q\n", cmd)
		return exitBadRequest
	}
}

// dispatchConfig handles the `config get` / `config set` subcommands. set builds a
// partial api.SetConfig from only the flags the user actually passed (via
// fs.Visit), so an omitted flag leaves that setting unchanged and a 0 rate stays a
// real value rather than being read as "unset".
func dispatchConfig(args []string, c *client, asJSON bool, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, "gidm: config requires a subcommand: config get | config set [flags]")
		return exitBadRequest
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "get":
		if code := noArgs(stderr, "config get", rest); code != exitOK {
			return code
		}
		return runCommand(c, asJSON, stdout, stderr, api.NewGetConfigRequest(),
			func(w io.Writer, r api.Response) error { return renderConfig(w, r, asJSON) })
	case "set":
		fs := flag.NewFlagSet("config set", flag.ContinueOnError)
		fs.SetOutput(stderr)
		dir := fs.String("dir", "", "default download directory (absolute)")
		segments := fs.Int("segments", 0, "default segments per download (1..64)")
		priority := fs.String("priority", "", "default priority: low | normal | high")
		maxRate := fs.Int("max-rate", 0, "global bandwidth cap in bytes/sec (0 = unlimited)")
		perDownloadMaxRate := fs.Int("per-download-max-rate", 0, "default per-download cap in bytes/sec (0 = unlimited)")
		if err := fs.Parse(rest); err != nil {
			return exitBadRequest
		}
		if fs.NArg() != 0 {
			_, _ = fmt.Fprintln(stderr, "gidm: config set takes only flags, no positional arguments")
			return exitBadRequest
		}
		set := map[string]bool{}
		fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
		if len(set) == 0 {
			_, _ = fmt.Fprintln(stderr, "gidm: config set needs at least one flag (e.g. --max-rate=1048576)")
			return exitBadRequest
		}
		var sc api.SetConfig
		if set["dir"] {
			sc.DownloadDir = dir
		}
		if set["segments"] {
			sc.SegmentsPerDownload = segments
		}
		if set["priority"] {
			p := api.Priority(*priority)
			sc.DefaultPriority = &p
		}
		if set["max-rate"] {
			sc.MaxRate = maxRate
		}
		if set["per-download-max-rate"] {
			sc.PerDownloadMaxRate = perDownloadMaxRate
		}
		if err := api.ValidateSetConfig(sc); err != nil {
			_, _ = fmt.Fprintln(stderr, "gidm:", err)
			return exitBadRequest
		}
		return runCommand(c, asJSON, stdout, stderr, api.NewSetConfigRequest(sc),
			func(w io.Writer, r api.Response) error { return renderConfig(w, r, asJSON) })
	default:
		_, _ = fmt.Fprintf(stderr, "gidm: unknown config subcommand %q (use get or set)\n", sub)
		return exitBadRequest
	}
}

// runCommand runs one request/response round-trip and renders the result. It owns the
// uniform mapping from transport errors and api error responses to exit codes so
// every subcommand behaves identically.
func runCommand(c *client, asJSON bool, stdout, stderr io.Writer, req api.Request, render func(io.Writer, api.Response) error) int {
	resp, err := c.do(context.Background(), req)
	if err != nil {
		return transportExit(stderr, c.socket, err)
	}
	if !resp.OK {
		return apiErrorExit(stderr, resp)
	}
	if err := render(stdout, resp); err != nil {
		_, _ = fmt.Fprintln(stderr, "gidm:", err)
		return exitGeneric
	}
	return exitOK
}

// transportExit prints a clear message for a dial/IO failure and returns the
// matching exit code, including a hint to start gidmd when it is not running.
func transportExit(stderr io.Writer, socket string, err error) int {
	switch {
	case errors.Is(err, errDaemonNotRunning):
		_, _ = fmt.Fprintf(stderr, "gidm: cannot reach gidmd on %q: %v\n", socket, err)
		_, _ = fmt.Fprintln(stderr, "hint: is the daemon running? start it with: gidmd")
		return exitDaemonNotUp
	case errors.Is(err, errTimeout):
		_, _ = fmt.Fprintf(stderr, "gidm: %v\n", err)
		return exitTimeout
	default:
		_, _ = fmt.Fprintf(stderr, "gidm: %v\n", err)
		return exitGeneric
	}
}

// apiErrorExit prints the daemon's human message and maps its stable error code
// to an exit code.
func apiErrorExit(stderr io.Writer, resp api.Response) int {
	msg := "unknown error"
	code := api.CodeInternal
	if resp.Error != nil {
		msg = resp.Error.Message
		code = resp.Error.Code
	}
	_, _ = fmt.Fprintln(stderr, "gidm:", msg)
	switch code {
	case api.CodeNotFound:
		return exitNotFound
	case api.CodeBadRequest:
		return exitBadRequest
	case api.CodeUnsupportedVersion, api.CodeInternal:
		return exitGeneric
	default:
		return exitGeneric
	}
}

// oneArg validates that rest holds exactly one positional argument and returns
// it; otherwise it prints a usage line and a non-zero code.
func oneArg(stderr io.Writer, cmd, arg string, rest []string) (string, int) {
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(rest); err != nil {
		return "", exitBadRequest
	}
	if fs.NArg() != 1 {
		_, _ = fmt.Fprintf(stderr, "gidm: %s requires exactly one argument: %s %s\n", cmd, cmd, arg)
		return "", exitBadRequest
	}
	return fs.Arg(0), exitOK
}

// twoArgs validates that rest holds exactly two positional arguments and returns
// them; otherwise it prints a usage line and a non-zero code.
func twoArgs(stderr io.Writer, cmd, args string, rest []string) (string, string, int) {
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(rest); err != nil {
		return "", "", exitBadRequest
	}
	if fs.NArg() != 2 {
		_, _ = fmt.Fprintf(stderr, "gidm: %s requires exactly two arguments: %s %s\n", cmd, cmd, args)
		return "", "", exitBadRequest
	}
	return fs.Arg(0), fs.Arg(1), exitOK
}

// noArgs validates that a subcommand takes no positional arguments.
func noArgs(stderr io.Writer, cmd string, rest []string) int {
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(rest); err != nil {
		return exitBadRequest
	}
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintf(stderr, "gidm: %s takes no arguments\n", cmd)
		return exitBadRequest
	}
	return exitOK
}

func usage(w io.Writer, gf *flag.FlagSet) {
	_, _ = fmt.Fprintf(w, "gidm %s — Go Internet Download Manager\n\n", version)
	_, _ = fmt.Fprintln(w, "Usage: gidm [global flags] <command> [args]")
	_, _ = fmt.Fprintln(w, "\nCommands:")
	_, _ = fmt.Fprintln(w, "  add [flags] <url>          queue a download and print its id")
	_, _ = fmt.Fprintln(w, "      flags: --priority low|normal|high  --dir <abs path>")
	_, _ = fmt.Fprintln(w, "             --filename <name>  --segments <n>")
	_, _ = fmt.Fprintln(w, "  list                       list all downloads")
	_, _ = fmt.Fprintln(w, "  status <id>                show one download (alias: get)")
	_, _ = fmt.Fprintln(w, "  pause <id>                 pause a download")
	_, _ = fmt.Fprintln(w, "  resume <id>                resume a paused download")
	_, _ = fmt.Fprintln(w, "  restart <id>               re-download from scratch (discards partial progress)")
	_, _ = fmt.Fprintln(w, "  rm <id>                    remove a download (deletes it; a completed file is kept)")
	_, _ = fmt.Fprintln(w, "  set-priority <id> <L>      change priority (L: low|normal|high)")
	_, _ = fmt.Fprintln(w, "  set-rate [--max-rate=N] <id>  cap one download to N bytes/sec (0 removes)")
	_, _ = fmt.Fprintln(w, "  config get                 show the daemon's runtime settings")
	_, _ = fmt.Fprintln(w, "  config set [flags]         change settings (--dir --segments --priority")
	_, _ = fmt.Fprintln(w, "                             --max-rate --per-download-max-rate)")
	_, _ = fmt.Fprintln(w, "\nGlobal flags:")
	gf.PrintDefaults()
}
