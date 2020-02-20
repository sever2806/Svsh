package svsh

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/sbinet/pstree"
)

// Runit implements the Supervisor interface, providing support for the
// runit init system (http://smarden.org/runit/)
type Runit struct {
	BaseDir   string
	DebugMode bool
}

func (o *Runit) fullSvcs(ss []string) []string {
	return mapStrings(ss, func(s string) string {
		return filepath.Join(o.BaseDir, s)
	})
}

var lookupDirs = []string{
	"/etc/sv",
	"/etc/service",
	"/service",
}

// DefaultRunitDir is the default service directory used by runit. Traditionally,
// /etc/service was used by default, but versions 1.9.0 changed the default to
// /service. runit still recommends /etc/service for FHS compliant systems, so
// this implementation uses /etc/service if it exists, and /service otherwise,
// when a specific directory is not provided by the user.
func (o *Runit) FindDefaultDir() string {
	for _, dir := range lookupDirs {
		if _, err := os.Stat(dir); err == nil {
			return dir
		}
	}

	return ""
}

var (
	runitStatusRegexp = regexp.MustCompile(`^([^:]+):[^:]+:(?: \(pid (\d+)\))? (\d+s)`)
	runitLoggerRegexp = regexp.MustCompile(`log: \(pid (\d+)\)`)
)

func (o *Runit) Status() (svcs []Service, err error) {
	dirs, err := serviceDirs(o.BaseDir)
	if err != nil {
		return svcs, fmt.Errorf("failed finding service directories: %w", err)
	}

	for _, dir := range dirs {
		svcName := filepath.Base(dir)

		raw, err := o.runCmd("status", dir)
		if err != nil {
			if o.DebugMode {
				return svcs, fmt.Errorf("failed reading status of %q: %w", svcName, err)
			}

			continue
		}

		matches := runitStatusRegexp.FindSubmatch(raw)
		if len(matches) != 4 {
			if o.DebugMode {
				return svcs, fmt.Errorf("failed parsing %q status output: %q", svcName, raw)
			}

			continue
		}

		svc := Service{
			Name: svcName,
		}

		switch string(matches[1]) {
		case "run":
			svc.Status = StatusUp
		case "down":
			svc.Status = StatusDown
		case "backoff":
			svc.Status = StatusBackoff
		case "disabled":
			svc.Status = StatusDisabled
		default:
			if o.DebugMode {
				return svcs, fmt.Errorf("failed parsing %q status: %q", svcName, matches[1])
			}

			svc.Status = StatusUnknown
		}

		if len(matches[2]) > 0 {
			svc.Pid, err = strconv.Atoi(string(matches[2]))
			if err != nil && o.DebugMode {
				return svcs, fmt.Errorf("failed parsing %q pid %q: %s", svcName, matches[2], err)
			}
		}

		if len(matches[3]) > 0 {
			svc.Duration, err = time.ParseDuration(string(matches[3]))
			if err != nil && o.DebugMode {
				return svcs, fmt.Errorf("failed parsing %q duration %q: %s", svcName, matches[3], err)
			}
		}

		svcs = append(svcs, svc)
	}

	return svcs, nil
}

func (o *Runit) Start(svcs ...string) error {
	_, err := o.runCmd("up", o.fullSvcs(svcs)...)
	return err
}

func (o *Runit) Stop(svcs ...string) error {
	_, err := o.runCmd("down", o.fullSvcs(svcs)...)
	return err
}

func (o *Runit) Restart(svcs ...string) error {
	_, err := o.runCmd("quit", o.fullSvcs(svcs)...)
	return err
}

func (o *Runit) Signal(signal os.Signal, svcs ...string) error {
	var cmd string

	switch signal {
	case syscall.SIGSTOP:
		cmd = "pause"
	case syscall.SIGCONT:
		cmd = "cont"
	case syscall.SIGHUP:
		cmd = "hup"
	case syscall.SIGALRM:
		cmd = "alarm"
	case syscall.SIGINT:
		cmd = "interrupt"
	case syscall.SIGQUIT:
		cmd = "quit"
	case syscall.SIGUSR1:
		cmd = "1"
	case syscall.SIGUSR2:
		cmd = "2"
	case syscall.SIGTERM:
		cmd = "term"
	case syscall.SIGKILL:
		cmd = "kill"
	default:
		return ErrUnsupportedSignal
	}

	_, err := o.runCmd(cmd, o.fullSvcs(svcs)...)

	return err
}

func (o *Runit) Fg(svc string) error {
	// find the pid of the logging process
	txt, err := o.runCmd("status", filepath.Join(o.BaseDir, svc))
	if err != nil {
		return fmt.Errorf("failed getting service status: %w", err)
	}

	matches := runitLoggerRegexp.FindSubmatch(txt)
	if len(matches) != 2 {
		return fmt.Errorf("failed parsing logger process status: %w", err)
	}

	pid, err := strconv.Atoi(string(matches[1]))
	if err != nil {
		return fmt.Errorf("failed parsing logger process pid %q: %w", matches[1], err)
	}

	file, err := findLogFile(pid)
	if err != nil {
		return fmt.Errorf("failed finding log file: %w", err)
	} else if file == "" {
		return fmt.Errorf("no log file found")
	}

	cmd := exec.Command("tail", "-f", file)
	cmd.Stdout = os.Stdout

	err = cmd.Start()
	if err != nil {
		return fmt.Errorf("failed starting tail: %w", err)
	}

	go func() {
		c := make(chan os.Signal, 1)
		signal.Notify(c, syscall.SIGINT, syscall.SIGTERM)

		cmd.Process.Signal(<-c) // nolint: errcheck
	}()

	err = cmd.Wait()
	if err != nil {
		if !strings.HasPrefix(err.Error(), "signal:") {
			return fmt.Errorf("failed tailing log: %w", err)
		}
	}

	return nil
}

func (o *Runit) Terminate() error {
	// we need to find the pid of the runsvdir process, and we have no choice
	// but to go over the system's process tree and finding it by name
	tree, err := pstree.New()
	if err != nil {
		return fmt.Errorf("failed fetching process tree: %w", err)
	}

	for pid, proc := range tree.Procs {
		if strings.Contains(proc.Name, fmt.Sprintf("runsvdir %s", o.BaseDir)) {
			err = syscall.Kill(pid, syscall.SIGHUP)
			if err != nil {
				return fmt.Errorf("failed killing runsvdir process %d: %w", pid, err)
			}
		}
	}

	return nil
}

func (o *Runit) Rescan() error {
	return ErrUnsupportedCommand
}

func (o *Runit) runCmd(subCmd string, args ...string) (output []byte, err error) {
	full := append([]string{subCmd}, args...)
	cmd := exec.Command("sv", full...)
	cmd.Env = []string{fmt.Sprintf("SVDIR=%s", o.BaseDir)}

	return cmd.CombinedOutput()
}
