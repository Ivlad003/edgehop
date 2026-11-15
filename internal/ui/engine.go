package ui

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const maxLogLines = 400

// engine runs the actual mouse and keyboard sharing as a child process
// (`edgehop run --child`). Keeping it in its own process means that stopping
// it releases every hook and device for certain, and that a crash in a
// platform backend cannot take the settings page down with it.
type engine struct {
	exe  string
	args []string // extra arguments, e.g. -v

	mu       sync.Mutex
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	done     chan struct{}
	running  bool
	stopping bool
	since    time.Time
	exit     string          // why it stopped, if it stopped by itself
	lastErr  string          // last "edgehop: ..." line it printed
	status   json.RawMessage // last status report
	logs     []string
}

func (e *engine) log(line string) {
	line = strings.TrimRight(line, "\r\n")
	if line == "" {
		return
	}
	e.mu.Lock()
	e.logs = append(e.logs, line)
	if len(e.logs) > maxLogLines {
		e.logs = append(e.logs[:0], e.logs[len(e.logs)-maxLogLines:]...)
	}
	if strings.HasPrefix(line, "edgehop: ") {
		e.lastErr = strings.TrimPrefix(line, "edgehop: ")
	}
	e.mu.Unlock()
}

// read consumes one output stream of the child. Lines on stdout that are
// JSON objects are status reports; everything else is log output.
func (e *engine) read(r io.Reader, statusStream bool) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if statusStream && strings.HasPrefix(line, "{") && json.Valid([]byte(line)) {
			e.mu.Lock()
			e.status = json.RawMessage(line)
			e.mu.Unlock()
			continue
		}
		e.log(line)
	}
}

// start launches the engine with the given config file.
func (e *engine) start(configPath string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.running {
		return nil
	}
	if e.exe == "" {
		return errors.New("the edgehop program file could not be located")
	}
	args := append([]string{"run", "--child", "-c", configPath}, e.args...)
	cmd := exec.Command(e.exe, args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan struct{})
	e.cmd, e.stdin, e.done = cmd, stdin, done
	e.running, e.stopping, e.since = true, false, time.Now()
	e.exit, e.lastErr, e.status = "", "", nil

	var readers sync.WaitGroup
	readers.Add(2)
	go func() { defer readers.Done(); e.read(stdout, true) }()
	go func() { defer readers.Done(); e.read(stderr, false) }()
	go func() {
		readers.Wait() // all output must be read before Wait closes the pipes
		err := cmd.Wait()
		e.mu.Lock()
		e.running = false
		e.status = nil
		if !e.stopping {
			switch {
			case e.lastErr != "":
				e.exit = e.lastErr
			case err != nil:
				e.exit = "stopped unexpectedly (" + err.Error() + ")"
			default:
				e.exit = "stopped"
			}
		}
		e.mu.Unlock()
		close(done)
	}()
	return nil
}

// command sends a request to the running engine, as one JSON line on its
// standard input.
func (e *engine) command(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.running || e.stdin == nil {
		return errors.New("edgehop is not running - start it first")
	}
	_, err = e.stdin.Write(append(data, '\n'))
	return err
}

// stop asks the engine to shut down cleanly (it watches its stdin) and kills
// it if it does not comply.
func (e *engine) stop() {
	e.mu.Lock()
	if !e.running {
		e.mu.Unlock()
		return
	}
	e.stopping = true
	stdin, cmd, done := e.stdin, e.cmd, e.done
	e.mu.Unlock()
	stdin.Close()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		cmd.Process.Kill()
		<-done
	}
}

type engineState struct {
	Running bool            `json:"running"`
	Since   string          `json:"since,omitempty"`
	Exit    string          `json:"exit,omitempty"`
	Status  json.RawMessage `json:"status,omitempty"`
}

func (e *engine) snapshot() (engineState, []string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	st := engineState{Running: e.running, Exit: e.exit, Status: e.status}
	if e.running {
		st.Since = e.since.Format(time.RFC3339)
	}
	return st, append([]string(nil), e.logs...)
}
