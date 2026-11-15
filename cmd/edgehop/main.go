// Command edgehop shares one mouse and keyboard between several computers:
// push the cursor against the edge of a screen and it continues on the next
// computer, taking the keyboard with it.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/Ivlad003/edgehop/internal/client"
	"github.com/Ivlad003/edgehop/internal/clip"
	"github.com/Ivlad003/edgehop/internal/config"
	"github.com/Ivlad003/edgehop/internal/input"
	"github.com/Ivlad003/edgehop/internal/keys"
	"github.com/Ivlad003/edgehop/internal/layout"
	"github.com/Ivlad003/edgehop/internal/logx"
	"github.com/Ivlad003/edgehop/internal/rview"
	"github.com/Ivlad003/edgehop/internal/secure"
	"github.com/Ivlad003/edgehop/internal/server"
	"github.com/Ivlad003/edgehop/internal/ui"
	"github.com/Ivlad003/edgehop/internal/wincap"
)

// Set at build time with -ldflags "-X main.version=... -X main.repoURL=...".
var (
	version = "dev"
	repoURL = ""
)

const usage = `edgehop %s - one mouse and keyboard for several computers

Usage:
  edgehop                              open the settings page in the browser:
                                       set this computer up, start and stop
  edgehop run [-c FILE] [-v]           run in the terminal with the saved
                                       settings, without the settings page

  edgehop init server [options]        create the settings on the computer
                                       that has the mouse and keyboard
      --name NAME      this screen's name (default: host name)
      --row A,B,C      all screens from left to right, including this one
      --port N         port to listen on (default %d)
  edgehop init client --server HOST --key KEY [--name NAME]
                                       create the settings on a computer that
                                       should be controlled
  edgehop info                         show screens, addresses, settings file
  edgehop keygen                       print a new random key
  edgehop version

Options for the settings page: --port N (default %d), --no-browser, --no-start.
Options for init: -c FILE (where to write the settings), --force (overwrite).

While another computer has the cursor, %s brings it back.
`

func printUsage(w io.Writer) {
	fmt.Fprintf(w, usage, version, config.DefaultPort, ui.DefaultPort, config.DefaultHotkey)
}

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int {
	// Started without arguments (for example by a double click): open the
	// settings page. Flags without a command keep meaning "run".
	cmd := "ui"
	switch {
	case len(args) > 0 && !strings.HasPrefix(args[0], "-"):
		cmd, args = args[0], args[1:]
	case len(args) > 0:
		cmd = "run"
	}
	var err error
	switch cmd {
	case "ui":
		err = cmdUI(args)
	case "run":
		err = cmdRun(args)
	case "init":
		err = cmdInit(args)
	case "info":
		err = cmdInfo(args)
	case "keygen":
		fmt.Println(secure.NewKey())
	case "selftest":
		err = cmdSelfTest(args)
	case "version":
		fmt.Printf("edgehop %s %s/%s\n", version, runtime.GOOS, runtime.GOARCH)
	case "help":
		printUsage(os.Stdout)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", cmd)
		printUsage(os.Stderr)
		return 2
	}
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		fmt.Fprintf(os.Stderr, "edgehop: %v\n", err)
		return 1
	}
	return 0
}

func newFlags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.Usage = func() { printUsage(fs.Output()) }
	return fs
}

func configFlag(fs *flag.FlagSet) *string {
	var path string
	fs.StringVar(&path, "c", "", "settings file")
	fs.StringVar(&path, "config", "", "settings file")
	return &path
}

// ---------------------------------------------------------------------------
// ui: the settings page

func cmdUI(args []string) error {
	fs := newFlags("ui")
	path := configFlag(fs)
	port := fs.Int("port", ui.DefaultPort, "port of the settings page")
	noBrowser := fs.Bool("no-browser", false, "do not open the browser")
	noStart := fs.Bool("no-start", false, "do not start sharing automatically")
	verbose := fs.Bool("v", false, "verbose log")
	noInput := fs.Bool("no-input", false, "do not touch the mouse or keyboard (network test)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *path == "" {
		*path = config.DefaultPath()
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	var engineArgs []string
	if *verbose {
		engineArgs = append(engineArgs, "-v")
	}
	if *noInput {
		engineArgs = append(engineArgs, "--no-input")
	}
	srv, err := ui.New(ui.Options{
		Addr:       fmt.Sprintf("127.0.0.1:%d", *port),
		ConfigPath: *path,
		Version:    version,
		RepoURL:    repoURL,
		Exe:        exe,
		EngineArgs: engineArgs,
		AutoStart:  !*noStart,
	})
	if err != nil {
		return err
	}
	if err := srv.Listen(); err != nil {
		// Started a second time (for example by another double click): show
		// the page of the copy that is already running instead of failing.
		if alreadyRunning(*port) {
			fmt.Println("edgehop is already running; opening its settings page.")
			if !*noBrowser {
				ui.OpenBrowser(srv.URL())
			}
			return nil
		}
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	fmt.Printf("edgehop %s\n\n", version)
	fmt.Printf("Settings page:  %s\n\n", srv.URL())
	fmt.Println("Keep this window open while edgehop is in use. Press Ctrl+C to quit.")
	if !*noBrowser {
		if err := ui.OpenBrowser(srv.URL()); err != nil {
			fmt.Println("Could not open the browser - open the link above yourself.")
		}
	}
	return srv.Run(ctx)
}

// alreadyRunning reports whether an edgehop settings page answers on the port.
func alreadyRunning(port int) bool {
	c := http.Client{Timeout: time.Second}
	resp, err := c.Get(fmt.Sprintf("http://127.0.0.1:%d/api/state", port))
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
	// Without the access cookie an edgehop page answers exactly like this.
	return resp.StatusCode == http.StatusUnauthorized && strings.Contains(string(body), "unauthorized")
}

// ---------------------------------------------------------------------------
// run

func cmdRun(args []string) error {
	fs := newFlags("run")
	path := configFlag(fs)
	verbose := fs.Bool("v", false, "verbose output")
	noInput := fs.Bool("no-input", false, "do not touch the mouse or keyboard (network test)")
	child := fs.Bool("child", false, "started by the settings page: report status on stdout, stop when stdin closes")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *path == "" {
		*path = config.DefaultPath()
	}
	cfg, err := config.Load(*path)
	if err != nil {
		return err
	}
	logx.SetVerbose(*verbose)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	cmds := make(chan childCommand, 8)
	if *child {
		// The settings page holds our stdin open and sends commands on it,
		// one JSON object per line. When it closes the pipe - or disappears -
		// shut down cleanly.
		go func() {
			sc := bufio.NewScanner(os.Stdin)
			sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
			for sc.Scan() {
				var cmd childCommand
				if json.Unmarshal(sc.Bytes(), &cmd) == nil && cmd.Cmd != "" {
					select {
					case cmds <- cmd:
					default:
					}
				}
			}
			stop()
		}()
	}

	w, h, _ := cfg.Size()
	opts := input.Options{
		HideCursor:   cfg.HideCursorEnabled(),
		Width:        w,
		Height:       h,
		PointerSpeed: cfg.PointerSpeed,
		PointerAccel: 1,
	}
	if cfg.PointerAccel != nil {
		opts.PointerAccel = *cfg.PointerAccel
	}
	logx.Infof("edgehop %s, settings %s", version, *path)
	if cfg.Role == "server" {
		return runServer(ctx, cfg, opts, *noInput, *child, cmds)
	}
	return runClient(ctx, cfg, opts, *noInput, *child, cmds)
}

// openClipboard returns the system clipboard to share, or nil when sharing
// is switched off or this computer offers no way to reach the clipboard.
func openClipboard(cfg *config.Config, noInput bool) (clip.Clipboard, string) {
	if noInput || !cfg.ClipboardEnabled() {
		return nil, ""
	}
	cb, err := clip.New()
	if err != nil {
		if errors.Is(err, clip.ErrUnavailable) {
			logx.Warnf("clipboard sharing is off: install xclip (X11) or wl-clipboard (Wayland) to enable it")
		} else {
			logx.Warnf("clipboard sharing is off: %v", err)
		}
		return nil, ""
	}
	dir, err := os.UserCacheDir()
	if err != nil {
		dir = os.TempDir()
	}
	logx.Infof("clipboard: shared, text and files up to %d MB (%s)", cfg.ClipboardLimit()>>20, cb.Describe())
	return cb, filepath.Join(dir, "edgehop", "clipboard")
}

// reportStatus prints the status as one JSON line whenever it changes.
func reportStatus(ctx context.Context, get func() any) {
	last := ""
	t := time.NewTicker(300 * time.Millisecond)
	defer t.Stop()
	for {
		if data, err := json.Marshal(get()); err == nil && string(data) != last {
			last = string(data)
			fmt.Println(last)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

type screenStatus struct {
	Name      string  `json:"name"`
	Self      bool    `json:"self"`
	Connected bool    `json:"connected"`
	OS        string  `json:"os,omitempty"`
	W         int     `json:"w,omitempty"`
	H         int     `json:"h,omitempty"`
	UDP       bool    `json:"udp,omitempty"`
	Files     bool    `json:"files,omitempty"` // files can be sent to it
	View      bool    `json:"view,omitempty"`  // its windows can be shown here
	RTTMs     float64 `json:"rttMs,omitempty"`
}

type serverStatus struct {
	Role    string         `json:"role"`
	Name    string         `json:"name"`
	Port    int            `json:"port"`
	Focus   string         `json:"focus"` // "" while the cursor is on this computer
	Hotkey  string         `json:"hotkey,omitempty"`
	Screens []screenStatus `json:"screens"`
	// Pending are computers that have the right key but are not in the layout.
	Pending []server.UnknownClient `json:"pending"`
	// Clipboard describes the last transfer (clipboard or files), if any.
	Clipboard string `json:"clipboard,omitempty"`
	// View is set while remote windows (experimental) are on.
	View *viewStatus `json:"view,omitempty"`
	sendStatus
}

// viewStatus tells the settings page where the windows of the other
// computers can be opened.
type viewStatus struct {
	URL string `json:"url"`
}

type clientStatus struct {
	Role      string `json:"role"`
	Name      string `json:"name"`
	Server    string `json:"server"`
	Connected bool   `json:"connected"`
	Active    bool   `json:"active"`          // the cursor is on this computer
	Error     string `json:"error,omitempty"` // why it is not connected
	Clipboard string `json:"clipboard,omitempty"`
	// ServerName is the server's screen name; Files tells whether files can
	// be sent to it.
	ServerName string `json:"serverName,omitempty"`
	Files      bool   `json:"files,omitempty"`
	// View: the server may show and control this computer's windows.
	View bool `json:"view,omitempty"`
	sendStatus
}

// childCommand is a request from the settings page to the running engine.
type childCommand struct {
	Cmd string `json:"cmd"` // "send"
	To  string `json:"to"`  // the screen to send to
	Dir string `json:"dir"` // a folder whose content is sent, then removed
}

// sendStatus is the part of the status about files sent on request.
type sendStatus struct {
	Sending   string `json:"sending,omitempty"`   // what is being sent right now
	SendError string `json:"sendError,omitempty"` // why the last attempt failed
	SendDone  uint64 `json:"sendDone,omitempty"`  // how many send requests are finished
	Downloads string `json:"downloads,omitempty"` // where received files are saved
}

// sender carries out "send" commands one after the other and remembers how
// the last one went.
type sender struct {
	sending   atomic.Value // string
	sendError atomic.Value // string
	done      atomic.Uint64
	downloads string
}

func (s *sender) status() sendStatus {
	a, _ := s.sending.Load().(string)
	b, _ := s.sendError.Load().(string)
	return sendStatus{Sending: a, SendError: b, SendDone: s.done.Load(), Downloads: s.downloads}
}

// run executes commands until ctx ends. send transfers the given paths to
// the named screen.
func (s *sender) run(ctx context.Context, cmds <-chan childCommand, send func(to string, paths []string) error) {
	for {
		select {
		case <-ctx.Done():
			return
		case cmd := <-cmds:
			if cmd.Cmd != "send" || cmd.Dir == "" {
				continue
			}
			entries, err := os.ReadDir(cmd.Dir)
			var paths []string
			for _, e := range entries {
				paths = append(paths, filepath.Join(cmd.Dir, e.Name()))
			}
			sort.Strings(paths)
			if err == nil && len(paths) == 0 {
				err = errors.New("no files to send")
			}
			if err == nil {
				s.sendError.Store("")
				s.sending.Store(fmt.Sprintf("%d item(s) to %s", len(paths), cmd.To))
				err = send(cmd.To, paths)
				s.sending.Store("")
			}
			if err != nil {
				s.sendError.Store(err.Error())
				logx.Warnf("files: sending to %s failed: %v", cmd.To, err)
			}
			os.RemoveAll(cmd.Dir) // the copy made for sending is no longer needed
			s.done.Add(1)
		}
	}
}

// screenOrder lists the layout's screens: the row first, then the rest.
func screenOrder(cfg *config.Config, lay *layout.Layout) []string {
	seen := map[string]bool{}
	var out []string
	for _, n := range append(append([]string{}, cfg.Row...), lay.Names()...) {
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	return out
}

func runServer(ctx context.Context, cfg *config.Config, opts input.Options, noInput, child bool, cmds <-chan childCommand) error {
	lay, err := cfg.Layout()
	if err != nil {
		return err
	}
	hotkey, err := cfg.Hotkey()
	if err != nil {
		return err
	}
	screens := map[string]server.ScreenOpts{}
	for name, s := range cfg.Screens {
		remap, err := keys.ParseRemap(s.Remap)
		if err != nil {
			return err
		}
		screens[name] = server.ScreenOpts{Scale: s.Scale, Remap: remap}
	}

	var capture input.Capture
	if noInput {
		capture = input.NewNullCapture(1920, 1080)
	} else if capture, err = input.NewCapture(opts); err != nil {
		return err
	}
	defer capture.Close()

	cb, clipDir := openClipboard(cfg, noInput)
	srv := server.New(server.Config{
		Name: cfg.Name, Listen: cfg.Listen, Key: cfg.Key,
		Layout: lay, Screens: screens, Hotkey: hotkey, AutoScale: true,
		Clipboard: cb, ClipDir: clipDir, ClipLimit: cfg.ClipboardLimit(),
		DropDir: cfg.DownloadDir(),
	}, capture)
	files := &sender{downloads: cfg.DownloadDir()}
	go files.run(ctx, cmds, srv.SendFiles)
	var view *viewStatus
	if cfg.RemoteWindows {
		viewer := &rview.Viewer{
			Peers: func() []rview.Peer {
				var out []rview.Peer
				for _, p := range srv.ViewPeers() {
					out = append(out, rview.Peer{Name: p.Name, OS: p.OS})
				}
				return out
			},
			Open: func(peer string) (rview.Conn, error) {
				ch, err := srv.OpenView(peer)
				if err != nil {
					return nil, err
				}
				return ch, nil
			},
			Remap: srv.ViewRemap,
		}
		if err := viewer.Listen(); err != nil {
			logx.Warnf("remote windows are not available: %v", err)
		} else {
			go viewer.Serve(ctx)
			view = &viewStatus{URL: viewer.URL()}
			logx.Infof("remote windows (experimental) are on: the settings page opens the windows of the other computers")
			if !child {
				logx.Infof("remote windows: %s", viewer.URL())
			}
		}
	}
	logx.Infof("files sent to this computer are saved in %s", cfg.DownloadDir())
	if err := srv.Listen(); err != nil {
		return err
	}
	cw, ch := capture.Size()
	logx.Infof("server %q, desktop %dx%d, listening on port %d (TCP and UDP)", cfg.Name, cw, ch, srv.Addr().Port)
	logx.Infof("input: %s", capture.Describe())
	if ips := ui.LocalIPs(); len(ips) > 0 {
		logx.Infof("this computer's addresses: %s", strings.Join(ips, ", "))
	}
	order := screenOrder(cfg, lay)
	if len(order) <= 1 {
		logx.Warnf("no other screens are configured yet - add them to the layout")
	} else {
		logx.Infof("layout:\n%s", strings.TrimRight(lay.Describe(), "\n"))
		logx.Infof("waiting for the other computers; %s returns the cursor here", hotkeyText(cfg))
	}
	if child {
		go reportStatus(ctx, func() any {
			st := serverStatus{Role: "server", Name: cfg.Name, Port: srv.Addr().Port, Focus: srv.Focus(), Pending: srv.Unknown(), Clipboard: srv.ClipboardStatus(), View: view, sendStatus: files.status()}
			if hotkey.Key != 0 {
				st.Hotkey = hotkeyText(cfg)
			}
			clients := map[string]server.ClientInfo{}
			for _, c := range srv.Clients() {
				clients[c.Name] = c
			}
			for _, n := range order {
				s := screenStatus{Name: n, Self: n == cfg.Name}
				if c, ok := clients[n]; ok {
					s.Connected, s.OS, s.W, s.H, s.UDP, s.RTTMs = true, c.OS, c.W, c.H, c.UDP, c.RTTMs
					s.Files, s.View = c.Files, c.View
				}
				st.Screens = append(st.Screens, s)
			}
			return st
		})
	}
	return srv.Serve(ctx)
}

func hotkeyText(cfg *config.Config) string {
	if cfg.ReleaseHotkey != nil {
		if s := strings.TrimSpace(*cfg.ReleaseHotkey); s == "" || strings.EqualFold(s, "none") {
			return "(no release hotkey configured)"
		}
		return *cfg.ReleaseHotkey
	}
	return config.DefaultHotkey
}

func runClient(ctx context.Context, cfg *config.Config, opts input.Options, noInput, child bool, cmds <-chan childCommand) error {
	var inj input.Injector
	var err error
	if noInput {
		w, h := opts.Width, opts.Height
		if w == 0 || h == 0 {
			w, h = 1920, 1080
		}
		inj = &input.NullInjector{W: w, H: h}
	} else if inj, err = input.NewInjector(opts); err != nil {
		return err
	}
	defer inj.Close()
	w, h := inj.Size()
	logx.Infof("client %q, desktop %dx%d, server %s", cfg.Name, w, h, cfg.Server)
	logx.Infof("input: %s", inj.Describe())
	cb, clipDir := openClipboard(cfg, noInput)
	var view *rview.Source
	if cfg.RemoteWindows {
		if err := wincap.Supported(); err != nil {
			logx.Warnf("remote windows: %v", err)
		} else {
			view = &rview.Source{Windows: wincap.System{}, Injector: inj, Name: cfg.Name, OS: runtime.GOOS}
			logx.Infof("remote windows (experimental) are on: the server may show and control this computer's windows")
		}
	}
	c := client.New(client.Config{
		Name: cfg.Name, Server: cfg.Server, Key: cfg.Key,
		Clipboard: cb, ClipDir: clipDir, ClipLimit: cfg.ClipboardLimit(),
		DropDir: cfg.DownloadDir(), View: view,
	}, inj)
	files := &sender{downloads: cfg.DownloadDir()}
	go files.run(ctx, cmds, func(_ string, paths []string) error { return c.SendFiles(paths) })
	logx.Infof("files sent to this computer are saved in %s", cfg.DownloadDir())
	if child {
		go reportStatus(ctx, func() any {
			return clientStatus{Role: "client", Name: cfg.Name, Server: cfg.Server,
				Connected: c.Connected(), Active: c.Active(), Error: c.LastError(), Clipboard: c.ClipboardStatus(),
				ServerName: c.ServerName(), Files: c.CanSendFiles(), View: view != nil, sendStatus: files.status()}
		})
	}
	return c.Run(ctx)
}

// ---------------------------------------------------------------------------
// init

func cmdInit(args []string) error {
	if len(args) == 0 || (args[0] != "server" && args[0] != "client") {
		return errors.New("usage: edgehop init server [...]  or  edgehop init client --server HOST --key KEY")
	}
	role := args[0]
	fs := newFlags("init")
	pathFlag := configFlag(fs)
	name := fs.String("name", config.DefaultName(), "name of this screen")
	force := fs.Bool("force", false, "overwrite existing settings")
	row := fs.String("row", "", "screens from left to right, comma separated (server)")
	port := fs.Int("port", config.DefaultPort, "port to listen on (server)")
	key := fs.String("key", "", "shared key (client: required; server: generated if empty)")
	srvAddr := fs.String("server", "", "address of the server (client)")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	path := *pathFlag
	if path == "" {
		path = config.UserPath()
	}

	var text string
	if role == "server" {
		if *key == "" {
			*key = secure.NewKey()
		}
		var names []string
		for _, n := range strings.Split(*row, ",") {
			if n = strings.TrimSpace(n); n != "" {
				names = append(names, n)
			}
		}
		text = config.ServerTemplate(*name, *key, *port, names)
	} else {
		if *srvAddr == "" || *key == "" {
			return errors.New("init client needs --server HOST and --key KEY (both are printed by `edgehop init server`)")
		}
		text = config.ClientTemplate(*name, *key, *srvAddr)
	}
	if _, err := config.Parse(text); err != nil {
		return err
	}
	if err := config.Write(path, text, *force); err != nil {
		return err
	}
	fmt.Printf("Created %s\n\n", path)

	if role == "client" {
		fmt.Printf("This computer is the screen %q.\n", *name)
		fmt.Printf("Make sure %q is part of the layout on the server, then start:  edgehop\n", *name)
		return nil
	}
	host := "SERVER-ADDRESS"
	ips := ui.LocalIPs()
	if len(ips) > 0 {
		host = ips[0]
	}
	if *port != config.DefaultPort {
		host = net.JoinHostPort(host, fmt.Sprint(*port))
	}
	fmt.Printf("This computer is the server, screen %q.\n\n", *name)
	fmt.Println("1. On every other computer run (with a different --name on each):")
	fmt.Printf("\n     edgehop init client --server %s --key %s --name NAME\n\n", host, *key)
	if len(ips) > 1 {
		fmt.Printf("   (this computer's addresses: %s)\n\n", strings.Join(ips, ", "))
	}
	fmt.Printf("2. Open %s and list all screen names from left to right in `row`\n", path)
	fmt.Println("   (or start `edgehop` and arrange them on the settings page).")
	fmt.Println("3. Start `edgehop` on every computer.")
	return nil
}

// ---------------------------------------------------------------------------
// info, selftest

func cmdInfo(args []string) error {
	fs := newFlags("info")
	pathFlag := configFlag(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	path := *pathFlag
	if path == "" {
		path = config.DefaultPath()
	}
	fmt.Printf("edgehop %s on %s/%s\n", version, runtime.GOOS, runtime.GOARCH)
	fmt.Printf("host name:  %s\n", config.DefaultName())
	if ips := ui.LocalIPs(); len(ips) > 0 {
		fmt.Printf("addresses:  %s\n", strings.Join(ips, ", "))
	}
	desk := layout.NewDesktop(input.Monitors())
	fmt.Printf("desktop:    %dx%d\n", desk.Bounds.W, desk.Bounds.H)
	for i, m := range desk.Monitors {
		fmt.Printf("  monitor %d: %dx%d at %d,%d\n", i+1, m.W, m.H, m.X, m.Y)
	}
	fmt.Printf("settings:   %s\n", path)
	cfg, err := config.Load(path)
	if err != nil {
		fmt.Printf("  %v\n", err)
		return nil
	}
	fmt.Printf("  role %s, name %q\n", cfg.Role, cfg.Name)
	if cfg.Role == "client" {
		fmt.Printf("  server %s\n", cfg.Server)
		return nil
	}
	fmt.Printf("  listen %s\n", cfg.Listen)
	if lay, err := cfg.Layout(); err == nil {
		fmt.Print(indent(lay.Describe(), "  "))
	}
	return nil
}

func indent(s, prefix string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i := range lines {
		lines[i] = prefix + strings.TrimLeft(lines[i], " ")
	}
	return strings.Join(lines, "\n") + "\n"
}

func cmdSelfTest(args []string) error {
	fs := newFlags("selftest")
	grab := fs.Bool("grab", false, "also take over the mouse and keyboard for a fraction of a second")
	if err := fs.Parse(args); err != nil {
		return err
	}
	var out io.Writer = os.Stdout
	if err := input.SelfTest(out, *grab); err != nil {
		return fmt.Errorf("self test failed: %w", err)
	}
	fmt.Fprintln(out, "self test passed")
	return nil
}
