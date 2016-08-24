// Copyright 2015 Daniel Theophanes.
// Use of this source code is governed by a zlib-style
// license that can be found in the LICENSE file.package service

package service

import (
	"bytes"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"

	"bitbucket.org/kardianos/osext"
	"github.com/getlantern/winsvc/eventlog"
	"github.com/getlantern/winsvc/mgr"
	"github.com/getlantern/winsvc/svc"
)

const version = "Windows Service"

type windowsService struct {
	Config

	errSync      sync.Mutex
	stopStartErr error
}

type windowsSystem struct{}

func (windowsSystem) String() string {
	return version
}

var system = windowsSystem{}

func newService(c Config) (*windowsService, error) {
	ws := &windowsService{
		Config: c,
	}
	return ws, nil
}

func (ws *windowsService) String() string {
	return ws.Name
}

func (ws *windowsService) setError(err error) {
	ws.errSync.Lock()
	defer ws.errSync.Unlock()
	ws.stopStartErr = err
}

func (ws *windowsService) getError() error {
	ws.errSync.Lock()
	defer ws.errSync.Unlock()
	return ws.stopStartErr
}

func (ws *windowsService) Execute(args []string, r <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
	const cmdsAccepted = svc.AcceptStop | svc.AcceptShutdown
	changes <- svc.Status{State: svc.StartPending}

	if err := ws.Config.Start(); err != nil {
		ws.setError(err)
		return true, 1
	}

	changes <- svc.Status{State: svc.Running, Accepts: cmdsAccepted}
loop:
	for {
		c := <-r
		switch c.Cmd {
		case svc.Interrogate:
			changes <- c.CurrentStatus
		case svc.Stop, svc.Shutdown:
			changes <- svc.Status{State: svc.StopPending}
			if ws.Config.Stop != nil {
				if err := ws.Config.Stop(); err != nil {
					ws.setError(err)
					return true, 2
				}
			}
			break loop
		default:
			continue loop
		}
	}

	return false, 0
}

func (ws *windowsService) InstallOrUpdateRequired() (bool, error) {
	if true {
		return true, nil
	}
	m, err := mgr.Connect()
	if err != nil {
		return false, err
	}
	defer m.Disconnect()

	s, oldCfg, err := ws.existingSvcAndConfig(m)
	if err != nil {
		return false, err
	}
	if s == nil {
		return true, nil
	}

	cfg, err := ws.buildConfig()
	if err != nil {
		return false, err
	}

	return !reflect.DeepEqual(cfg, oldCfg), nil
}

func (ws *windowsService) InstallOrUpdate() (bool, error) {
	m, err := mgr.Connect()
	if err != nil {
		return false, fmt.Errorf("Unable to connect to service manager: %v", err)
	}
	defer m.Disconnect()

	cfg, err := ws.buildConfig()
	if err != nil {
		return false, fmt.Errorf("Unable to build config: %v", err)
	}

	s, oldCfg, err := ws.existingSvcAndConfig(m)
	if err != nil {
		return false, fmt.Errorf("Unable to get existing service and config: %v", err)
	}
	if s != nil && reflect.DeepEqual(cfg, oldCfg) {
		// Service already exists and doesn't need updating
		return false, nil
	}

	if s == nil {
		exepath, err := osext.Executable()
		if err != nil {
			return false, fmt.Errorf("Unable to determine executable: %v", err)
		}

		binPath := &bytes.Buffer{}
		// Quote exe path in case it contains a string.
		binPath.WriteRune('"')
		binPath.WriteString(exepath)
		binPath.WriteRune('"')

		// Arguments are encoded with the binary path to service.
		// Enclose arguments in quotes. Escape quotes with a backslash.
		for _, arg := range ws.Arguments {
			binPath.WriteRune(' ')
			binPath.WriteString(`"`)
			binPath.WriteString(strings.Replace(arg, `"`, `\"`, -1))
			binPath.WriteString(`"`)
		}
		s, err = m.CreateService(ws.Name, binPath.String(), cfg)
		if err != nil {
			return false, fmt.Errorf("Unable to create service: %v", err)
		}
		defer s.Close()
		return false, ws.doStart(m)
	} else {
		defer s.Close()
		err = s.UpdateConfig(cfg)
		if err != nil {
			return false, fmt.Errorf("Unable to update config: %v", err)
		}
		return true, nil
	}
}

func (ws *windowsService) buildConfig() (mgr.Config, error) {
	cfg := mgr.Config{
		DisplayName:      ws.Name,
		Description:      ws.Name,
		StartType:        mgr.StartAutomatic,
		ServiceStartName: ".\\LocalSystem",
	}

	return cfg, nil
}

func (ws *windowsService) existingSvcAndConfig(m *mgr.Mgr) (*mgr.Service, mgr.Config, error) {
	s, err := m.OpenService(ws.Name)
	if err != nil {
		return nil, mgr.Config{}, nil
	}

	oldCfg, err := s.Config()
	if err != nil {
		return s, mgr.Config{}, err
	}

	return s, oldCfg, nil
}

func (ws *windowsService) Uninstall() error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	s, err := m.OpenService(ws.Name)
	if err != nil {
		return fmt.Errorf("service %s is not installed", ws.Name)
	}
	defer s.Close()
	err = s.Delete()
	if err != nil {
		return err
	}
	err = eventlog.Remove(ws.Name)
	if err != nil {
		return fmt.Errorf("RemoveEventLogSource() failed: %s", err)
	}
	return nil
}

func (ws *windowsService) Run() error {
	ws.setError(nil)

	// Return error messages from start and stop routines
	// that get executed in the Execute method.
	// Guarded with a mutex as it may run a different thread
	// (callback from windows).
	runErr := svc.Run(ws.Name, ws)
	startStopErr := ws.getError()
	if startStopErr != nil {
		return startStopErr
	}
	if runErr != nil {
		return runErr
	}
	return nil
}

func (ws *windowsService) Start() error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	return nil
}

func (ws *windowsService) doStart(m *mgr.Mgr) error {
	s, err := m.OpenService(ws.Name)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.Start([]string{})
}

func (ws *windowsService) Stop() error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()

	s, err := m.OpenService(ws.Name)
	if err != nil {
		return err
	}
	defer s.Close()
	_, err = s.Control(svc.Stop)
	return err
}

func (ws *windowsService) Restart() error {
	err := ws.Stop()
	if err != nil {
		return err
	}
	time.Sleep(50 * time.Millisecond)
	return ws.Start()
}
