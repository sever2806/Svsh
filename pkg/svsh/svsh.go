package svsh

import (
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"
)

var (
	ErrUnsupportedCommand = errors.New("command is not supported by the supervisor")
	ErrUnsupportedSignal  = errors.New("sending this signal is unsupported by the supervisor")
)

const Version = "2.0.0"

type Status uint8

const (
	StatusUp Status = iota
	StatusDown
	StatusResetting
	StatusBackoff
	StatusDisabled
	StatusUnknown
)

var statusNames = map[Status]string{
	StatusUp:        "up",
	StatusDown:      "down",
	StatusResetting: "resetting",
	StatusBackoff:   "backoff",
	StatusDisabled:  "disabled",
	StatusUnknown:   "unknown",
}

func (s Status) String() string {
	n, ok := statusNames[s]
	if !ok {
		return "unknown"
	}

	return n
}

type Service struct {
	Name     string
	Status   Status
	Pid      int
	Duration time.Duration
}

type Supervisor interface {
	FindDefaultDir() string
	Status() ([]Service, error)
	Start(services ...string) error
	Stop(services ...string) error
	Restart(services ...string) error
	Signal(signal os.Signal, services ...string) error
	Fg(service string) error
	Rescan() error
	Terminate() error
}

func findLogFile(pid int) (file string, err error) {
	dir := filepath.Join("/proc", strconv.Itoa(pid))

	exe, err := os.Readlink(filepath.Join(dir, "/exe"))
	if err != nil {
		return file, fmt.Errorf("failed reading symbolic link: %w", err)
	}

	switch filepath.Base(exe) {
	case "tinylog", "s6-log", "svlogd", "multilog":
		// look for a link to a /current file under /proc/<pid>/fd
		fd := filepath.Join(dir, "fd")

		links, err := ioutil.ReadDir(fd)
		if err != nil {
			return file, fmt.Errorf("failed reading fd directory %q: %w", fd, err)
		}

		for _, link := range links {
			if link.Mode()&(1<<uint(32-1-4)) != 0 {
				target, err := os.Readlink(filepath.Join(dir, "fd", link.Name()))
				if err != nil {
					return file, fmt.Errorf("failed reading link %q: %w", link.Name(), err)
				}

				if filepath.Base(target) == "current" {
					return target, nil
				}
			}
		}
	}

	return file, nil
}

func serviceDirs(baseDir string) (dirs []string, err error) {
	files, err := ioutil.ReadDir(baseDir)
	if err != nil {
		return dirs, fmt.Errorf("failed reading base directory %q: %w", baseDir, err)
	}

	dirs = make([]string, 0, len(files))

	for _, file := range files {
		if file.IsDir() {
			dirs = append(dirs, file.Name())
		}
	}

	sort.Strings(dirs)

	return dirs, nil
}

func mapStrings(input []string, fn func(string) string) []string {
	output := make([]string, len(input))

	for i := range input {
		output[i] = fn(input[i])
	}

	return output
}
