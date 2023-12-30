// Copyright 2015 Daniel Theophanes.
// Use of this source code is governed by a zlib-style
// license that can be found in the LICENSE file.package service

package service

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"text/template"
	"time"

	"github.com/kardianos/osext"
)

const maxPathSize = 32 * 1024

const version = "Darwin Launchd"

type darwinSystem struct{}

func (ls darwinSystem) String() string {
	return version
}

var system = darwinSystem{}

func isInteractive() (bool, error) {
	// TODO: The PPID of Launchd is 1. The PPid of a service process should match launchd's PID.
	return os.Getppid() != 1, nil
}

func newService(c Config) (*darwinLaunchdService, error) {
	s := &darwinLaunchdService{
		Config:          c,
		serviceFilePath: filepath.Join("/Library/LaunchDaemons/", c.Name+".plist"),
	}
	if s.Program == "" {
		program, err := osext.Executable()
		if err != nil {
			return nil, fmt.Errorf("Unable to determin program: %v", err)
		}
		s.Program = program
	}

	return s, nil
}

type darwinLaunchdService struct {
	Config

	serviceFilePath string
}

func (s *darwinLaunchdService) InstallOrUpdateRequired() (bool, error) {
	tmpFile, err := s.prepareTmpFile()
	if tmpFile != "" {
		defer os.Remove(tmpFile)
	}
	if err != nil {
		return false, err
	}

	return s.differsFromInstalled(tmpFile)
}

func (s *darwinLaunchdService) InstallOrUpdate() (bool, error) {
	tmpFile, err := s.prepareTmpFile()
	if tmpFile != "" {
		defer os.Remove(tmpFile)
	}
	if err != nil {
		return false, err
	}

	installOrUpdateRequired, err := s.differsFromInstalled(tmpFile)
	if err != nil {
		return installOrUpdateRequired, fmt.Errorf("Unable to determine if new configuration differs from old: %v", err)
	}

	// Move config into place
	err = os.Rename(tmpFile, s.serviceFilePath)
	if err != nil {
		return false, fmt.Errorf("Unable to move service configuration to: %v", err)
	}

	// Change owner to root
	err = os.Chown(s.serviceFilePath, 0, 0)
	if err != nil {
		return false, fmt.Errorf("Unable to change owner to root: %v", err)
	}

	err = commandAsRoot("launchctl", "load", s.serviceFilePath).Run()
	if err != nil {
		return false, fmt.Errorf("Unable to load service: %v", err)
	}

	return true, nil
}

func (s *darwinLaunchdService) prepareTmpFile() (string, error) {
	tmpFile, err := ioutil.TempFile("", "service.plist")
	if err != nil {
		return "", fmt.Errorf("Unable to create temporary service configuration: %v", err)
	}
	defer tmpFile.Close()

	functions := template.FuncMap{
		"bool": func(v bool) string {
			if v {
				return "true"
			}
			return "false"
		},
	}
	t := template.Must(template.New("launchdConfig").Funcs(functions).Parse(launchdConfig))
	err = t.Execute(tmpFile, s)
	if err != nil {
		return "", fmt.Errorf("Unable to process service configuration template: %v", err)
	}
	err = tmpFile.Chmod(0644)
	if err != nil {
		return "", fmt.Errorf("Unable to chmod temp file: %v", err)
	}
	err = tmpFile.Close()
	if err != nil {
		return "", fmt.Errorf("Unable to close temp file: %v", err)
	}

	return tmpFile.Name(), nil
}

func (s *darwinLaunchdService) differsFromInstalled(tmpFile string) (bool, error) {
	_, err := os.Stat(s.serviceFilePath)
	if err == nil {
		// Compare new and old configs
		old, err := ioutil.ReadFile(s.serviceFilePath)
		if err != nil {
			return false, fmt.Errorf("Unable to read existing launchd configuration at %v for comparing: %v", s.serviceFilePath, err)
		}

		updated, err := ioutil.ReadFile(tmpFile)
		if err != nil {
			return false, fmt.Errorf("Unable to read updated launchd configuration at %v for comparing: %v", tmpFile, err)
		}

		if bytes.Compare(old, updated) == 0 {
			return false, nil
		}

		log.Printf("Old and new configurations at %v and %v differ", s.serviceFilePath, tmpFile)
		time.Sleep(5 * time.Hour)
	} else if !os.IsNotExist(err) {
		return false, fmt.Errorf("Unable to stat existing launchd configuration at %v: %v", s.serviceFilePath, err)
	} else {
		log.Println("No old configuration found")
	}

	return true, nil
}

func (s *darwinLaunchdService) Uninstall() error {
	err := exec.Command("sudo", "launchctl", "unload", s.serviceFilePath).Run()
	if err != nil {
		return fmt.Errorf("Unable to unload service prior to uninstalling: %v", err)
	}

	return os.Remove(s.serviceFilePath)
}

func (s *darwinLaunchdService) Start() error {
	return commandAsRoot("launchctl", "start", s.Name).Run()
}

func (s *darwinLaunchdService) Stop() error {
	return commandAsRoot("launchctl", "stop", s.Name).Run()
}

func (s *darwinLaunchdService) Restart() error {
	err := s.Stop()
	if err != nil {
		return err
	}
	time.Sleep(50 * time.Millisecond)
	return s.Start()
}

func (s *darwinLaunchdService) Run() error {
	var err error

	err = s.Config.Start()
	if err != nil {
		return err
	}

	var sigChan = make(chan os.Signal, 3)

	signal.Notify(sigChan, os.Interrupt, os.Kill)

	<-sigChan

	if s.Config.Stop == nil {
		return nil
	}

	return s.Config.Stop()
}

func commandAsRoot(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Credential: &syscall.Credential{
			Uid: 0,
			Gid: 0,
		},
	}
	return cmd
}

var launchdConfig = `<?xml version='1.0' encoding='UTF-8'?>
<!DOCTYPE plist PUBLIC "-//Apple Computer//DTD PLIST 1.0//EN"
"http://www.apple.com/DTDs/PropertyList-1.0.dtd" >
<plist version='1.0'>
<dict>
<key>Label</key><string>{{html .Name}}</string>
<key>Program</key><string>{{html .Program}}</string>
<key>ProgramArguments</key>
<array>{{range .Config.Arguments}}
        <string>{{html .}}</string>
{{end}}</array>
{{if .WorkingDirectory}}<key>WorkingDirectory</key><string>{{html .WorkingDirectory}}</string>{{end}}
<key>KeepAlive</key>
<dict>
	<key>SuccessfulExit</key>
	<false/>
</dict>
<key>RunAtLoad</key><true/>
<key>Disabled</key><false/>
<key>UserName</key>
<string>root</string>
<key>GroupName</key>
<string>wheel</string>
<key>InitGroups</key>
<true/>
</dict>
</plist>
`
