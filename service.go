// Copyright 2015 Daniel Theophanes.
// Use of this source code is governed by a zlib-style
// license that can be found in the LICENSE file.package service

// Package service provides a simple way to create a system service.
// Currently supports Windows, Linux/(systemd | Upstart | SysV), and OSX/Launchd.
package service // import "github.com/getlantern/service"

import (
	"errors"
)

// Config provides the setup for a Service. The Name field is required.
type Config struct {
	Name             string   // Required name of the service. No spaces suggested.
	Privileged       bool     // If true, service will run as root/Administrator/etc
	Program          string   // The name of the program, defaults to the current program
	Arguments        []string // Run with arguments.
	WorkingDirectory string   // Optional, service working directory
}

// Service represents a service that can be run or controlled.
type Service interface {
	// Start signals to the OS service manager the given service should start.
	Start() error

	// Stop signals to the OS service manager the given service should stop.
	Stop() error

	// Restart signals to the OS service manager the given service should stop
	// then start.
	Restart() error

	// InstalLOrUpdateRequired checks whether the service needs to be installed
	// or udpated.
	InstallOrUpdateRequired() (bool, error)

	// InstallOrUpdate installs or updates the given service to the OS service manager. If
	// the service doesn't yet exist, it is created. If it already exists, the
	// existing service is updated. If additional privileges are needed, the
	// user is prompted with an escalation dialog.
	//
	// Returns true if the service was installed or updated, false if it was
	// left alone.
	InstallOrUpdate(run func() error) (bool, error)

	// Uninstall uninstalls the given service from the OS service manager. This may require
	// greater rights. Will return an error if the service is not present.
	Uninstall() error
}

var errNameFieldRequired = errors.New("Config.Name field is required.")

// New creates a new service based on a service interface and configuration.
func New(c Config) (Service, error) {
	if len(c.Name) == 0 {
		return nil, errNameFieldRequired
	}
	return newService(c)
}

// Platform returns a description of the OS and service platform.
func Platform() string {
	return system.String()
}

// runningSystem represents the system and system's service being used.
type runningSystem interface {
	// String returns a description of the OS and service platform.
	String() string
}

// Be sure to implement each platform.
var _ runningSystem = system
