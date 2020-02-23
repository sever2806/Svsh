package svsh

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"syscall"
	"time"
)

type S6 struct {
	BaseDir   string
	DebugMode bool
}

func (o *S6) fullService(s string) string {
	return filepath.Join(o.BaseDir, s)
}

var (
	s6StatusRegexp = regexp.MustCompile(`(up|down) \(([^\)]+)\) (\d+)/`)
	s6PidRegexp    = regexp.MustCompile(`pid (\d+)`)
)

func (o *S6) Status() (svcs []Service, err error) {
	dirs, err := serviceDirs(o.BaseDir)
	if err != nil {
		return svcs, fmt.Errorf("failed finding service directories: %w", err)
	}

	for _, dir := range dirs {
		svcName := filepath.Base(dir)

		raw, err := o.runCmd("s6-svstat", filepath.Join(o.BaseDir, dir))
		if err != nil {
			if o.DebugMode {
				return svcs, fmt.Errorf("failed reading status of %q: %w", svcName, err)
			}

			continue
		}

		matches := s6StatusRegexp.FindSubmatch(raw)
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
		case "up":
			svc.Status = StatusUp
		case "down":
			svc.Status = StatusDown
		default:
			if o.DebugMode {
				return svcs, fmt.Errorf("failed parsing %q status: %q", svcName, matches[1])
			}

			svc.Status = StatusUnknown
		}

		if len(matches[2]) > 0 {
			pm := s6PidRegexp.FindSubmatch(matches[2])
			if len(pm) == 2 {
				svc.Pid, err = strconv.Atoi(string(pm[1]))
				if err != nil && o.DebugMode {
					return svcs, fmt.Errorf("failed parsing %q pid %q: %s", svcName, pm[1], err)
				}
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

func (o *S6) Start(svcs ...string) error {
	for _, svc := range svcs {
		_, err := o.runCmd("s6-svc", "-u", o.fullService(svc))

		if err != nil {
			return fmt.Errorf("failed restarting %s: %s", svc, err)
		}
	}

	return nil
}

func (o *S6) Stop(svcs ...string) error {
	for _, svc := range svcs {
		_, err := o.runCmd("s6-svc", "-Dd", o.fullService(svc))

		if err != nil {
			return fmt.Errorf("failed restarting %s: %s", svc, err)
		}
	}

	return nil
}

func (o *S6) Restart(svcs ...string) error {
	for _, svc := range svcs {
		_, err := o.runCmd("s6-svc", "-q", o.fullService(svc))

		if err != nil {
			return fmt.Errorf("failed restarting %s: %s", svc, err)
		}
	}

	return nil
}

func (o *S6) Signal(signal os.Signal, svcs ...string) error {
	var cmd string

	switch signal {
	case syscall.SIGALRM:
		cmd = "-a"
	case syscall.SIGABRT:
		cmd = "-b"
	case syscall.SIGQUIT:
		cmd = "-q"
	case syscall.SIGHUP:
		cmd = "-h"
	case syscall.SIGKILL:
		cmd = "-k"
	case syscall.SIGTERM:
		cmd = "-t"
	case syscall.SIGINT:
		cmd = "-i"
	case syscall.SIGUSR1:
		cmd = "-1"
	case syscall.SIGUSR2:
		cmd = "-2"
	case syscall.SIGCONT:
		cmd = "-c"
	case syscall.SIGWINCH:
		cmd = "-y"
	default:
		return ErrUnsupportedSignal
	}

	for _, svc := range svcs {
		_, err := o.runCmd("s6-svc", cmd, o.fullService(svc))
		if err != nil {
			return fmt.Errorf("failed signaling %s: %s", svc, err)
		}
	}

	return nil
}

func (o *S6) Fg(svc string) error {
	// find the pid of the logging process
	txt, err := o.runCmd("s6-svstat", filepath.Join(o.BaseDir, svc, "log"))
	if err != nil {
		return fmt.Errorf("failed getting logger service status: %w", err)
	}

	matches := s6PidRegexp.FindSubmatch(txt)
	if len(matches) != 2 {
		return fmt.Errorf("failed parsing logger process status: %w", err)
	}

	pid, err := strconv.Atoi(string(matches[1]))
	if err != nil {
		return fmt.Errorf("failed parsing logger process pid %q: %w", matches[1], err)
	}

	return fgProc(pid)
}

func (o *S6) Terminate() error {
	_, err := o.runCmd("s6-svscanctl", "-t", o.BaseDir)

	return err
}

func (o *S6) Rescan() error {
	_, err := o.runCmd("s6-svscanctl", "-a", o.BaseDir)

	return err
}

func (o *S6) runCmd(cmdName, subCmd string, args ...string) (output []byte, err error) {
	return exec.
		Command(cmdName, append([]string{subCmd}, args...)...).
		CombinedOutput()
}
