// Command edgehop shares one mouse and keyboard between several computers:
// push the cursor against the edge of a screen and it continues on the next
// computer, taking the keyboard with it.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"runtime"
	"sort"
	"strings"
	"syscall"

	"edgehop/internal/client"
	"edgehop/internal/config"
	"edgehop/internal/input"
	"edgehop/internal/keys"
	"edgehop/internal/layout"
	"edgehop/internal/logx"
	"edgehop/internal/secure"
	"edgehop/internal/server"
)

const version = "0.1.0"

const usage = `edgehop %s - one mouse and keyboard for several computers

Usage:
  edgehop [run] [-c FILE] [-v]         start edgehop using the config file
  edgehop init server [options]        create the config on the computer that
                                       has the mouse and keyboard
      --name NAME      this screen's name (default: host name)
      --row A,B,C      all screens from left to right, including this one
      --port N         port to listen on (default %d)
  edgehop init client --server HOST --key KEY [--name NAME]
                                       create the config on a computer that
                                       should be controlled
  edgehop info                         show screens, addresses, config location
  edgehop keygen                       print a new random key
  edgehop version

Options for init: -c FILE (where to write the config), --force (overwrite).

While another computer has the cursor, %s brings it back.
`

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int {
	cmd := "run"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	var err error
	switch cmd {
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
		fmt.Printf(usage, version, config.DefaultPort, config.DefaultHotkey)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", cmd)
		fmt.Fprintf(os.Stderr, usage, version, config.DefaultPort, config.DefaultHotkey)
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
	fs.Usage = func() { fmt.Fprintf(fs.Output(), usage, version, config.DefaultPort, config.DefaultHotkey) }
	return fs
}

// ---------------------------------------------------------------------------
// run

func cmdRun(args []string) error {
	fs := newFlags("run")
	var path string
	fs.StringVar(&path, "c", "", "config file")
	fs.StringVar(&path, "config", "", "config file")
	verbose := fs.Bool("v", false, "verbose output")
	noInput := fs.Bool("no-input", false, "do not touch the mouse or keyboard (network test)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if path == "" {
		path = config.DefaultPath()
	}
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	logx.SetVerbose(*verbose)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

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
	logx.Infof("edgehop %s, config %s", version, path)
	if cfg.Role == "server" {
		return runServer(ctx, cfg, opts, *noInput)
	}
	return runClient(ctx, cfg, opts, *noInput)
}

func runServer(ctx context.Context, cfg *config.Config, opts input.Options, noInput bool) error {
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

	srv := server.New(server.Config{
		Name: cfg.Name, Listen: cfg.Listen, Key: cfg.Key,
		Layout: lay, Screens: screens, Hotkey: hotkey,
	}, capture)
	if err := srv.Listen(); err != nil {
		return err
	}
	cw, ch := capture.Size()
	logx.Infof("server %q, desktop %dx%d, listening on port %d (TCP and UDP)", cfg.Name, cw, ch, srv.Addr().Port)
	logx.Infof("input: %s", capture.Describe())
	if ips := localIPs(); len(ips) > 0 {
		logx.Infof("this computer's addresses: %s", strings.Join(ips, ", "))
	}
	others := 0
	for _, n := range lay.Names() {
		if n != cfg.Name {
			others++
		}
	}
	if others == 0 {
		logx.Warnf("no other screens are configured - list them in `row` in %s", cfg.Path())
	} else {
		logx.Infof("layout:\n%s", strings.TrimRight(lay.Describe(), "\n"))
		logx.Infof("waiting for the other computers; %s returns the cursor here", hotkeyText(cfg))
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

func runClient(ctx context.Context, cfg *config.Config, opts input.Options, noInput bool) error {
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
	return client.New(client.Config{Name: cfg.Name, Server: cfg.Server, Key: cfg.Key}, inj).Run(ctx)
}

// ---------------------------------------------------------------------------
// init

func cmdInit(args []string) error {
	if len(args) == 0 || (args[0] != "server" && args[0] != "client") {
		return errors.New("usage: edgehop init server [...]  or  edgehop init client --server HOST --key KEY")
	}
	role := args[0]
	fs := newFlags("init")
	var path string
	fs.StringVar(&path, "c", "", "config file")
	fs.StringVar(&path, "config", "", "config file")
	name := fs.String("name", config.DefaultName(), "name of this screen")
	force := fs.Bool("force", false, "overwrite an existing config")
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
		fmt.Printf("Make sure %q is listed in `row` in the server's config, then start:  edgehop\n", *name)
		return nil
	}
	host := "SERVER-ADDRESS"
	ips := localIPs()
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
	fmt.Printf("2. Open %s and list all screen names from left to right in `row`.\n", path)
	fmt.Println("3. Start `edgehop` on every computer.")
	return nil
}

// ---------------------------------------------------------------------------
// info, selftest

func cmdInfo(args []string) error {
	fs := newFlags("info")
	var path string
	fs.StringVar(&path, "c", "", "config file")
	fs.StringVar(&path, "config", "", "config file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if path == "" {
		path = config.DefaultPath()
	}
	fmt.Printf("edgehop %s on %s/%s\n", version, runtime.GOOS, runtime.GOARCH)
	fmt.Printf("host name:  %s\n", config.DefaultName())
	if ips := localIPs(); len(ips) > 0 {
		fmt.Printf("addresses:  %s\n", strings.Join(ips, ", "))
	}
	desk := layout.NewDesktop(input.Monitors())
	fmt.Printf("desktop:    %dx%d\n", desk.Bounds.W, desk.Bounds.H)
	for i, m := range desk.Monitors {
		fmt.Printf("  monitor %d: %dx%d at %d,%d\n", i+1, m.W, m.H, m.X, m.Y)
	}
	fmt.Printf("config:     %s\n", path)
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
	grab := fs.Bool("grab", false, "also take over the mouse for a fraction of a second")
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

// localIPs lists this computer's LAN addresses, private IPv4 ranges first.
func localIPs() []string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	var private, other []string
	for _, a := range addrs {
		ipn, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		ip := ipn.IP.To4()
		if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
			continue
		}
		if ip.IsPrivate() {
			private = append(private, ip.String())
		} else {
			other = append(other, ip.String())
		}
	}
	sort.Strings(private)
	sort.Strings(other)
	return append(private, other...)
}
