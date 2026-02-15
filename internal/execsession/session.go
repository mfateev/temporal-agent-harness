package execsession

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"

	"github.com/creack/pty"
)

// pollInterval is how often to check for new output during CollectOutput.
const pollInterval = 25 * time.Millisecond

// ErrStdinClosed is returned when writing to a pipe-mode session's stdin.
var ErrStdinClosed = errors.New("stdin is closed (pipe mode does not support write_stdin)")

// SessionOpts configures a new exec session.
type SessionOpts struct {
	ProcessID string
	Command   []string // [program, args...]
	Cwd       string
	Env       []string // Full environment (nil = inherit)
	TTY       bool
}

// ExecSession wraps a running process (PTY or pipes) with background output
// collection. Sessions persist in worker memory across activity calls.
//
// Maps to: codex-rs/core/src/unified_exec/process.rs UnifiedExecProcess
type ExecSession struct {
	ProcessID string
	Command   []string
	Cwd       string
	TTY       bool
	StartedAt time.Time
	LastUsed  time.Time

	cmd       *exec.Cmd
	ptyFile   *os.File       // PTY master (tty=true only)
	stdinPipe io.WriteCloser // Pipe stdin (tty=false only)
	outputBuf *HeadTailBuffer
	exitCode  atomic.Int32
	exited    atomic.Bool
	exitCh    chan struct{}   // Closed on process exit.
	readerWg  sync.WaitGroup // Tracks background read goroutines.
	mu        sync.Mutex
}

// StartSession spawns a process and returns a session for interacting with it.
// The process runs in PTY mode if opts.TTY is true, otherwise pipe mode.
func StartSession(opts SessionOpts) (*ExecSession, error) {
	if len(opts.Command) == 0 {
		return nil, errors.New("empty command")
	}

	s := &ExecSession{
		ProcessID: opts.ProcessID,
		Command:   opts.Command,
		Cwd:       opts.Cwd,
		TTY:       opts.TTY,
		StartedAt: time.Now(),
		LastUsed:  time.Now(),
		outputBuf: NewHeadTailBuffer(DefaultMaxBytes),
		exitCh:    make(chan struct{}),
	}
	// Sentinel: -1 means "not exited yet".
	s.exitCode.Store(-1)

	cmd := exec.Command(opts.Command[0], opts.Command[1:]...)
	if opts.Cwd != "" {
		cmd.Dir = opts.Cwd
	}
	if opts.Env != nil {
		cmd.Env = opts.Env
	}
	s.cmd = cmd

	if opts.TTY {
		if err := s.startPTY(cmd); err != nil {
			return nil, err
		}
	} else {
		if err := s.startPipes(cmd); err != nil {
			return nil, err
		}
	}

	// Background goroutine: wait for process exit.
	go s.waitForExit()

	return s, nil
}

func (s *ExecSession) startPTY(cmd *exec.Cmd) error {
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 24, Cols: 80})
	if err != nil {
		return err
	}
	s.ptyFile = ptmx

	// Background reader: PTY combines stdout+stderr.
	s.readerWg.Add(1)
	go s.readLoop(ptmx)
	return nil
}

func (s *ExecSession) startPipes(cmd *exec.Cmd) error {
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

	// Background readers: separate stdout and stderr.
	s.readerWg.Add(2)
	go s.readLoop(stdout)
	go s.readLoop(stderr)
	return nil
}

func (s *ExecSession) readLoop(r io.Reader) {
	defer s.readerWg.Done()
	buf := make([]byte, 8192)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			s.outputBuf.Push(buf[:n])
		}
		if err != nil {
			return
		}
	}
}

func (s *ExecSession) waitForExit() {
	// Wait for read goroutines to drain all output BEFORE calling cmd.Wait().
	// cmd.Wait() closes pipe read ends (see os/exec.Cmd.StdoutPipe docs:
	// "It is thus incorrect to call Wait before all reads from the pipe
	// have completed."). For pipes, readers get EOF when the child exits
	// (OS closes the write end). For PTY, readers get EIO when the slave
	// side closes. Either way, readers finish before we call Wait.
	s.readerWg.Wait()
	err := s.cmd.Wait()

	code := -1
	if err == nil {
		code = 0
	} else {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			code = exitErr.ExitCode()
		}
	}
	s.exitCode.Store(int32(code))
	s.exited.Store(true)
	close(s.exitCh)
}

// WriteStdin sends data to the process's stdin. Only supported in TTY mode.
func (s *ExecSession) WriteStdin(data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.TTY {
		return ErrStdinClosed
	}
	if s.ptyFile == nil {
		return ErrStdinClosed
	}
	_, err := s.ptyFile.Write(data)
	return err
}

// CollectOutput waits until the deadline for new output, returning whatever
// has been produced. If heartbeat is non-nil, it is called periodically
// during the wait (roughly every 5 seconds).
func (s *ExecSession) CollectOutput(deadline time.Time, heartbeat func(details ...interface{})) []byte {
	mark := s.outputBuf.TotalWritten()
	var collected []byte
	heartbeatInterval := 5 * time.Second
	lastHeartbeat := time.Now()

	for {
		now := time.Now()
		if now.After(deadline) {
			break
		}

		// Heartbeat periodically.
		if heartbeat != nil && now.Sub(lastHeartbeat) >= heartbeatInterval {
			heartbeat("collecting output")
			lastHeartbeat = now
		}

		// Check for new output.
		currentTotal := s.outputBuf.TotalWritten()
		if currentTotal > mark {
			snapshot := s.outputBuf.Snapshot()
			if len(snapshot) > 0 {
				collected = snapshot
			}
			mark = currentTotal
		}

		// If process exited (and all readers drained), grab final output and exit.
		// Because waitForExit waits for readerWg, s.HasExited()==true guarantees
		// all output has been pushed to the buffer.
		if s.HasExited() {
			if final := s.outputBuf.Snapshot(); len(final) > 0 {
				collected = final
			}
			break
		}

		time.Sleep(pollInterval)
	}

	// If we never got any incremental updates, grab the snapshot now.
	if collected == nil {
		if snapshot := s.outputBuf.Snapshot(); len(snapshot) > 0 {
			collected = snapshot
		}
	}

	s.mu.Lock()
	s.LastUsed = time.Now()
	s.mu.Unlock()

	return collected
}

// HasExited returns true if the process has terminated.
func (s *ExecSession) HasExited() bool {
	return s.exited.Load()
}

// ExitCode returns the exit code, or nil if the process is still running.
func (s *ExecSession) ExitCode() *int {
	if !s.exited.Load() {
		return nil
	}
	code := int(s.exitCode.Load())
	return &code
}

// Close terminates the process and cleans up resources.
func (s *ExecSession) Close() {
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	if s.ptyFile != nil {
		_ = s.ptyFile.Close()
	}
	if s.stdinPipe != nil {
		_ = s.stdinPipe.Close()
	}
}
