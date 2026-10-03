package instance

import (
	"errors"

	"golang.org/x/sys/windows"
)

// mutexName is in the Global namespace so an agent running as a service
// (session 0) and one in a user's session also exclude each other.
const mutexName = `Global\cinefin-playout`

// Acquire takes the machine-wide lock, or returns ErrRunning.
func Acquire() (*Lock, error) {
	name, err := windows.UTF16PtrFromString(mutexName)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateMutex(nil, false, name)
	switch {
	case errors.Is(err, windows.ERROR_ALREADY_EXISTS):
		windows.CloseHandle(h)
		return nil, ErrRunning
	case errors.Is(err, windows.ERROR_ACCESS_DENIED):
		// The mutex exists but belongs to another account (e.g. the service).
		return nil, ErrRunning
	case err != nil:
		return nil, err
	}
	return &Lock{release: func() { windows.CloseHandle(h) }}, nil
}
